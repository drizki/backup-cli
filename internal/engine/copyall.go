package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/drizki/backup-cli/internal/copyx"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/journal"
	"github.com/drizki/backup-cli/internal/manifest"
)

// copier carries what the workers share.
type copier struct {
	r      *run
	p      *plan
	jr     *journal.Journal
	src    func(rel string) (*os.File, error)
	prev   func(rel string) (*os.File, error)
	chown  bool
	done   atomic.Int64
	kept   atomic.Int64
	failMu sync.Mutex
	failed error
}

func (r *run) copyAll(ctx context.Context, p *plan) error {
	if !p.resumed {
		if err := os.Mkdir(p.partialDir, 0o700); err != nil {
			return exitcode.Failedf("create %s: %v", p.partialDir, err)
		}
	}
	jr, err := journal.Open(filepath.Join(p.partialDir, journal.Name), r.j.Name, r.id)
	if err != nil {
		return exitcode.Failedf("journal: %v", err)
	}
	defer func() { _ = jr.Close() }()
	srcRoot, err := os.OpenRoot(r.j.Source)
	if err != nil {
		return exitcode.Failedf("open source: %v", err)
	}
	defer func() { _ = srcRoot.Close() }()
	c := &copier{r: r, p: p, jr: jr, chown: copyx.IsRoot()}
	c.src = r.e.sourceOpener(srcRoot)
	closePrev, err := c.openPrevRoot()
	if err != nil {
		return err
	}
	defer closePrev()
	if err := c.makeTree(ctx); err != nil {
		return err
	}
	err = c.copyFiles(ctx)
	if kept := c.kept.Load(); kept > 0 {
		r.log.Infof("resume: kept %d verified file(s) from the partial backup", kept)
	}
	if err != nil {
		_ = jr.Sync()
	}
	return err
}

func (e *Engine) sourceOpener(root *os.Root) func(string) (*os.File, error) {
	if e.OpenSource != nil {
		return e.OpenSource
	}
	return func(rel string) (*os.File, error) { return copyx.OpenReadOnly(copyx.RootOpener(root, rel)) }
}

func (c *copier) openPrevRoot() (func(), error) {
	if c.r.e.OpenPrev != nil {
		c.prev = c.r.e.OpenPrev
		return func() {}, nil
	}
	if c.p.prev == nil {
		return func() {}, nil
	}
	root, err := os.OpenRoot(c.p.prev.dir)
	if err != nil {
		return nil, exitcode.Failedf("open previous backup: %v", err)
	}
	c.prev = func(rel string) (*os.File, error) { return copyx.OpenReadOnly(copyx.RootOpener(root, rel)) }
	return func() { _ = root.Close() }, nil
}

// makeTree creates directories and symlinks. Entries arrive parents first.
func (c *copier) makeTree(ctx context.Context) error {
	for _, e := range c.p.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.Path == manifest.RootPath || e.Type == manifest.TypeFile {
			continue
		}
		dst := filepath.Join(c.p.partialDir, filepath.FromSlash(e.Path))
		var err error
		if e.Type == manifest.TypeDir {
			err = copyx.MakeDir(dst)
		} else {
			err = copyx.Symlink(dst, e.LinkTarget, metaOf(e), c.chown)
		}
		if err != nil {
			return exitcode.Failedf("create %s: %v", e.Path, err)
		}
	}
	return nil
}

func metaOf(e manifest.Entry) copyx.Meta {
	return copyx.Meta{Mode: e.Mode, UID: e.UID, GID: e.GID, MtimeNS: e.MtimeNS}
}

// fail records the first error and stops the other workers.
func (c *copier) fail(err error, cancel context.CancelFunc) {
	c.failMu.Lock()
	defer c.failMu.Unlock()
	if c.failed == nil {
		c.failed = err
	}
	cancel()
}

func (c *copier) copyFiles(ctx context.Context) error {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan fileJob)
	var wg sync.WaitGroup
	for range c.r.workers() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := copyx.GetBuf()
			defer copyx.PutBuf(buf)
			for f := range work {
				if err := c.copyOne(wctx, *buf, f); err != nil {
					c.fail(err, cancel)
				}
			}
		}()
	}
	for _, f := range c.p.files {
		select {
		case work <- f:
			continue
		case <-wctx.Done():
		}
		break
	}
	close(work)
	wg.Wait()
	if c.failed != nil {
		return c.failed
	}
	if c.done.Load() < int64(len(c.p.files)) {
		return fmt.Errorf("stopped before all files were copied: %w", ctx.Err())
	}
	return nil
}
