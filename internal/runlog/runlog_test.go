package runlog

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

var lineRe = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z (INFO |WARN |ERROR) .+$`)

func TestFileAndTee(t *testing.T) {
	p := filepath.Join(t.TempDir(), "run.log")
	var tee bytes.Buffer
	l, err := Open(p, &tee)
	if err != nil {
		t.Fatal(err)
	}
	l.Now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 6_000_000, time.UTC) }
	l.Infof("hello %d", 1)
	l.Warnf("careful")
	l.Errorf("bad")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != tee.String() {
		t.Fatalf("file and tee differ:\n%s\n%s", b, tee.String())
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "2026-01-02T03:04:05.006Z INFO  hello 1") {
		t.Fatalf("%q", lines)
	}
	for _, ln := range lines {
		if !lineRe.MatchString(ln) {
			t.Errorf("bad line %q", ln)
		}
	}
}

func TestAppendsAcrossOpens(t *testing.T) {
	p := filepath.Join(t.TempDir(), "run.log")
	for range 2 {
		l, _ := Open(p, nil)
		l.Infof("x")
		_ = l.Close()
	}
	b, _ := os.ReadFile(p)
	if strings.Count(string(b), "\n") != 2 {
		t.Fatalf("%q", b)
	}
}

func TestMirrorWritesNoFile(t *testing.T) {
	var tee bytes.Buffer
	l := Mirror(&tee)
	l.Infof("only here")
	if !strings.Contains(tee.String(), "only here") || l.Close() != nil || l.WriteErrors() != 0 {
		t.Fatalf("%q", tee.String())
	}
	Mirror(nil).Infof("nowhere")
}

func TestConcurrentLinesStayWhole(t *testing.T) {
	p := filepath.Join(t.TempDir(), "run.log")
	l, _ := Open(p, nil)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				l.Infof("worker %d line", i)
			}
		}()
	}
	wg.Wait()
	_ = l.Close()
	b, _ := os.ReadFile(p)
	for _, ln := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if !lineRe.MatchString(ln) {
			t.Fatalf("interleaved line %q", ln)
		}
	}
}

func TestOpenErrorsAndWriteErrors(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "no", "run.log"), nil); err == nil {
		t.Fatal("missing directory accepted")
	}
	l, _ := Open(filepath.Join(t.TempDir(), "run.log"), nil)
	_ = l.Close()
	l.Infof("after close")
	if l.WriteErrors() != 1 {
		t.Fatalf("write errors %d", l.WriteErrors())
	}
}
