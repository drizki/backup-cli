package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/cli"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/manifest"
	"github.com/drizki/backup-cli/internal/retention"
	"github.com/drizki/backup-cli/internal/runlog"
	"github.com/drizki/backup-cli/internal/state"
)

// The chaos test re-executes the test binary as a child that runs a real
// backup with retention, and stops it at random points: SIGKILL by the parent,
// SIGKILL by the child itself at a chosen stage (after N copied files, after
// the commit, in the middle of pruning), or SIGTERM for a clean stop. The
// source changes between runs. After every run the invariants must hold.

const (
	chaosStateEnv = "BACKUP_CLI_CHAOS_STATE"
	chaosClockEnv = "BACKUP_CLI_CHAOS_CLOCK"
	chaosModeEnv  = "BACKUP_CLI_CHAOS_MODE"
	chaosFiles    = 90
)

// TestChaosChild is the child's body: one real run of the job "chaos".
func TestChaosChild(t *testing.T) {
	dir := os.Getenv(chaosStateEnv)
	if dir == "" {
		return // an ordinary test run, not a child
	}
	store := state.New(dir)
	j, err := store.LoadJob("chaos")
	if err != nil {
		os.Exit(90)
	}
	secs, _ := strconv.ParseInt(os.Getenv(chaosClockEnv), 10, 64)
	e := &Engine{Retention: retention.Func(store.Dir), Now: func() time.Time { return time.Unix(secs, 0) }}
	selfKill(e, os.Getenv(chaosModeEnv))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	err = e.Run(ctx, cli.EngineRequest{Store: store, Job: j, VerifySample: -1, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(exitcode.Code(err))
}

func die() { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL) }

// selfKill arms the engine's seams so the child kills itself at a stage.
func selfKill(e *Engine, mode string) {
	kind, arg, _ := strings.Cut(mode, ":")
	n, _ := strconv.Atoi(arg)
	switch kind {
	case "copy":
		var count atomic.Int64
		e.AfterCopy = func(string) {
			if int(count.Add(1)) == n {
				die()
			}
		}
	case "commit":
		e.PostCommit = func(string) { die() }
	case "prune":
		inner := e.Retention
		e.Retention = func(ctx context.Context, j job.Job, c []backupset.Backup, cur string, l *runlog.Log) error {
			go func() { time.Sleep(time.Duration(n) * time.Microsecond); die() }()
			return inner(ctx, j, c, cur, l)
		}
	}
}

// chaosPlan is what happens to one child.
type chaosPlan struct {
	mode  string        // self-kill stage, or ""
	after time.Duration // parent-sent signal delay; 0 means none
	sig   syscall.Signal
}

func drawPlan(rng *rand.Rand) chaosPlan {
	switch r := rng.IntN(100); {
	case r < 30:
		return chaosPlan{mode: fmt.Sprintf("copy:%d", 1+rng.IntN(70))}
	case r < 40:
		return chaosPlan{mode: "commit"}
	case r < 50:
		return chaosPlan{mode: fmt.Sprintf("prune:%d", rng.IntN(1500))}
	case r < 75:
		return chaosPlan{after: time.Duration(1+rng.IntN(120)) * time.Millisecond, sig: syscall.SIGKILL}
	case r < 85:
		return chaosPlan{after: time.Duration(1+rng.IntN(100)) * time.Millisecond, sig: syscall.SIGTERM}
	}
	return chaosPlan{} // runs to completion
}

// chaosWorld is one seed's directories.
type chaosWorld struct {
	t                  *testing.T
	root, src, dst, st string
	j                  job.Job
	clock              int64
	rng                *rand.Rand
	lastErr            bytes.Buffer // stderr of the latest child
}

func newChaosWorld(t *testing.T, seed int) *chaosWorld {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := &chaosWorld{t: t, root: root, src: filepath.Join(root, "src"), dst: filepath.Join(root, "bk"),
		st: filepath.Join(root, "state"), clock: 1_800_000_000, rng: rand.New(rand.NewPCG(uint64(seed), 7))}
	for _, d := range []string{w.src, w.dst, filepath.Join(w.dst, "other-dir")} {
		must(t, os.MkdirAll(d, 0o755))
	}
	must(t, os.WriteFile(filepath.Join(w.dst, "keep-me.txt"), []byte("not a backup"), 0o644))
	must(t, os.WriteFile(filepath.Join(w.dst, "other-dir", "x"), []byte("x"), 0o644))
	for i := range chaosFiles {
		p := w.file(i)
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, pattern(500+w.rng.IntN(30_000), byte(i)), 0o644))
	}
	must(t, os.WriteFile(filepath.Join(w.src, "big.bin"), pattern(10<<20, 9), 0o644))
	store := state.New(w.st)
	w.j = job.Job{Version: 1, Name: "chaos", Source: w.src, DstTemplate: filepath.Join(w.dst, "c-{timestamp}"), Keep: 1, Jobs: 4, VerifySamplePct: 5}
	must(t, store.Ensure())
	must(t, store.SaveJob(w.j))
	return w
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (w *chaosWorld) file(i int) string {
	return filepath.Join(w.src, fmt.Sprintf("d%d/f%03d", i%7, i))
}

// child runs one child under the plan and reports how it ended.
func (w *chaosWorld) child(p chaosPlan) (code int, signaled bool) {
	w.lastErr.Reset()
	w.clock += 3600 // every run gets its own backup name
	cmd := exec.Command(os.Args[0], "-test.run=^TestChaosChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), chaosStateEnv+"="+w.st, chaosClockEnv+"="+strconv.FormatInt(w.clock, 10), chaosModeEnv+"="+p.mode)
	cmd.Stderr = &w.lastErr
	must(w.t, cmd.Start())
	if p.after > 0 {
		time.Sleep(p.after)
		_ = cmd.Process.Signal(p.sig)
	}
	_ = cmd.Wait()
	ws := cmd.ProcessState.Sys().(syscall.WaitStatus)
	return ws.ExitStatus(), ws.Signaled()
}

// mutate changes the source as a live tree would.
func (w *chaosWorld) mutate() {
	p := w.file(w.rng.IntN(chaosFiles))
	switch w.rng.IntN(5) {
	case 0:
		_ = os.Remove(p)
	case 1:
		_ = os.WriteFile(p, pattern(500, byte(w.rng.IntN(200))), 0o644)
	case 2: // file becomes a directory
		_ = os.Remove(p)
		_ = os.MkdirAll(filepath.Join(p, "inner"), 0o755)
		_ = os.WriteFile(filepath.Join(p, "inner", "x"), []byte("x"), 0o644)
	case 3: // directory becomes a file
		_ = os.RemoveAll(filepath.Dir(p))
		_ = os.WriteFile(filepath.Dir(p), []byte("was a directory"), 0o644)
	default:
		_ = os.WriteFile(filepath.Join(w.src, "big.bin"), pattern(10<<20, byte(w.rng.IntN(250))), 0o644)
	}
}

// committedProblems fully verifies every committed backup, with the tool's own
// verifier and with sha256sum -c when it is installed.
func (w *chaosWorld) committedProblems() []string {
	var bad []string
	all, _ := backupset.Scan(w.j)
	sumBin, lookErr := exec.LookPath("sha256sum")
	for _, b := range backupset.OnlyCommitted(all) {
		rep, err := manifest.Verify(context.Background(), b.Path, manifest.VerifyOptions{SamplePct: 100})
		if err != nil || !rep.OK() || len(rep.Warnings) > 0 {
			bad = append(bad, fmt.Sprintf("%s: %v %v %v", b.Name, err, rep.Failures, rep.Warnings))
		}
		if lookErr != nil {
			continue
		}
		c := exec.Command(sumBin, "-c", "--quiet", manifest.SHA256Name)
		c.Dir = b.Path
		if out, err := c.CombinedOutput(); err != nil {
			bad = append(bad, fmt.Sprintf("%s: sha256sum -c: %v %s", b.Name, err, out))
		}
	}
	return bad
}

// bystanders describes the destination's unrelated entries, which retention
// and the sweep must never touch.
func (w *chaosWorld) bystanders() map[string]node {
	m := snap(w.t, filepath.Join(w.dst, "other-dir"))
	b, err := os.ReadFile(filepath.Join(w.dst, "keep-me.txt"))
	m["keep-me.txt"] = node{Typ: fmt.Sprintf("%v", err), Sum: string(b)}
	return m
}

func TestChaosKillsNeverBreakABackup(t *testing.T) {
	seeds, runs := 20, 6
	if testing.Short() {
		seeds, runs = 20, 3
	}
	var partials, signaled, finished atomic.Int64
	t.Run("seeds", func(t *testing.T) {
		for seed := range seeds {
			t.Run(fmt.Sprintf("seed%02d", seed), func(t *testing.T) {
				t.Parallel()
				w := newChaosWorld(t, seed)
				outside := snap(t, w.root, "src", "bk", "state")
				by := w.bystanders()
				for range runs {
					before := snap(t, w.src)
					code, sig := w.child(drawPlan(w.rng))
					w.afterRun(before, code, sig, &signaled, &finished)
					if m, _ := filepath.Glob(filepath.Join(w.dst, "*"+backupset.PartialSuffix)); len(m) > 0 {
						partials.Add(1)
					}
					w.mutate()
					w.mutate()
				}
				if d := diffSnaps(outside, snap(t, w.root, "src", "bk", "state")); d != "" {
					t.Errorf("something outside src/bk/state changed:\n%s", d)
				}
				if d := diffSnaps(by, w.bystanders()); d != "" {
					t.Errorf("unrelated entries of the destination changed:\n%s", d)
				}
				w.converge()
			})
		}
	})
	t.Logf("%d seeds x %d runs: %d runs ended by a signal, %d ran to the end, %d left a .partial", seeds, runs, signaled.Load(), finished.Load(), partials.Load())
}

// afterRun checks the invariants that must hold after every child.
func (w *chaosWorld) afterRun(before map[string]node, code int, sig bool, signaled, finished *atomic.Int64) {
	t := w.t
	t.Helper()
	switch {
	case sig:
		signaled.Add(1)
	case code == exitcode.OK || code == exitcode.Partial:
		finished.Add(1)
	default:
		t.Errorf("a run failed instead of being stopped: exit %d: %s", code, w.lastErr.String())
	}
	if d := diffSnaps(before, snap(t, w.src)); d != "" {
		t.Errorf("the source changed:\n%s", d)
	}
	for _, p := range w.committedProblems() {
		t.Errorf("committed backup is not valid: %s", p)
	}
}

// converge runs the job to completion and checks the result against the source.
func (w *chaosWorld) converge() {
	t := w.t
	if code, sig := w.child(chaosPlan{}); code != exitcode.OK || sig {
		t.Fatalf("the final run did not converge: exit %d, signaled %v: %s", code, sig, w.lastErr.String())
	}
	for _, p := range w.committedProblems() {
		t.Errorf("final backup is not valid: %s", p)
	}
	all, _ := backupset.Scan(w.j)
	com := backupset.OnlyCommitted(all)
	if len(com) != 1 {
		t.Fatalf("%d committed backups after the final run, want 1 (keep 1)", len(com))
	}
	if m, _ := filepath.Glob(filepath.Join(w.dst, "*"+backupset.PartialSuffix)); len(m) > 0 {
		t.Errorf("a .partial remains after the final run: %v", m)
	}
	if d := diffSnaps(snap(t, w.src), snap(t, com[0].Path, backupMeta...)); d != "" {
		t.Errorf("the final backup differs from the source:\n%s", d)
	}
}
