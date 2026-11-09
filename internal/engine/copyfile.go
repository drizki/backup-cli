package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/drizki/backup-cli/internal/copyx"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/journal"
)

// copyOne makes one file present, correct and journaled in the partial
// directory: kept from an earlier attempt, copied from the previous backup,
// or copied from the source.
func (c *copier) copyOne(ctx context.Context, buf []byte, f fileJob) error {
	e := &c.p.entries[f.idx]
	dst := filepath.Join(c.p.partialDir, filepath.FromSlash(e.Path))
	switch {
	case c.keepJournaled(ctx, buf, e.Path, dst, f):
		c.kept.Add(1)
	case f.prevSHA != "" && c.fromPrev(ctx, buf, f, dst):
		c.r.fromPrev.Add(1)
	default:
		if err := c.fromSource(ctx, buf, f, dst); err != nil {
			return err
		}
		c.r.fromSource.Add(1)
		c.r.srcBytes.Add(e.Size)
	}
	c.done.Add(1)
	if err := c.jr.Add(journal.Entry{Path: e.Path, Size: e.Size, MtimeNS: e.MtimeNS, SHA256: e.SHA256}); err != nil {
		return exitcode.Failedf("journal: %v", err)
	}
	return nil
}

// keepJournaled reuses a file completed by an interrupted run, after
// re-hashing it and re-applying its metadata.
func (c *copier) keepJournaled(ctx context.Context, buf []byte, rel, dst string, f fileJob) bool {
	je, ok := c.p.replay[rel]
	e := &c.p.entries[f.idx]
	if !ok || je.Size != e.Size || je.MtimeNS != e.MtimeNS {
		return false
	}
	size, sum, err := copyx.HashFile(ctx, dst, buf)
	if err != nil || size != je.Size || sum != je.SHA256 {
		c.r.log.Warnf("resume: %s did not verify; copying it again", rel)
		return false
	}
	if err := copyx.Apply(dst, metaOf(*e), c.chown); err != nil {
		return false
	}
	e.SHA256 = sum
	return true
}

// fromPrev copies an unchanged file from the previous backup and checks it
// against that backup's manifest. false means "use the source instead".
func (c *copier) fromPrev(ctx context.Context, buf []byte, f fileJob, dst string) bool {
	e := &c.p.entries[f.idx]
	if c.prev == nil {
		return false
	}
	src, err := c.prev(e.Path)
	if err != nil {
		c.r.warn("previous copy of %s cannot be read (%v); reading the source", e.Path, err)
		return false
	}
	defer func() { _ = src.Close() }()
	m := metaOf(*e)
	res, err := copyx.CopyFile(ctx, dst, src, copyx.FileOpts{Buf: buf, Meta: &m, Chown: c.chown})
	if ctx.Err() != nil {
		return false
	}
	if err != nil {
		c.r.warn("copying the previous copy of %s failed (%v); reading the source", e.Path, err)
		return false
	}
	if res.SHA256 != f.prevSHA || res.Size != e.Size {
		c.r.warn("BITROT: the previous copy of %s no longer matches its checksum; recopying from the source", e.Path)
		return false
	}
	e.SHA256 = res.SHA256
	return true
}

// fromSource copies a file from the source. A file that changes while it is
// copied is retried once; if it is still unstable the copy is kept and a
// warning is recorded.
func (c *copier) fromSource(ctx context.Context, buf []byte, f fileJob, dst string) error {
	e := &c.p.entries[f.idx]
	var res copyx.FileResult
	for attempt := 1; attempt <= 2; attempt++ {
		var err error
		if res, err = c.copySourceOnce(ctx, buf, e.Path, dst); err != nil {
			return err
		}
		if !res.Changed {
			break
		}
		if attempt == 1 {
			c.r.log.Warnf("%s changed while it was being copied; retrying", e.Path)
			continue
		}
		c.r.warn("unstable: %s kept changing while it was copied; the backup holds the content read last", e.Path)
	}
	e.Size, e.SHA256 = res.Size, res.SHA256
	e.MtimeNS, e.Mode, e.UID, e.GID = res.Meta.MtimeNS, res.Meta.Mode, res.Meta.UID, res.Meta.GID
	return nil
}

func (c *copier) copySourceOnce(ctx context.Context, buf []byte, rel, dst string) (copyx.FileResult, error) {
	src, err := c.src(rel)
	if err != nil {
		return copyx.FileResult{}, exitcode.Failedf("read %s: %v", rel, err)
	}
	defer func() { _ = src.Close() }()
	opts := copyx.FileOpts{Buf: buf, Chown: c.chown}
	if hook := c.r.e.AfterCopy; hook != nil {
		opts.AfterCopy = func() { hook(rel) }
	}
	res, err := copyx.CopyFile(ctx, dst, src, opts)
	if err == nil {
		return res, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return res, err
	}
	return res, exitcode.With(exitcode.Failed, fmt.Errorf("%s: %w", rel, err))
}
