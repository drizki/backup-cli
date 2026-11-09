package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/manifest"
)

// makeBackup writes a small committed backup directly (no engine).
const backupName = "data-20260101T000000Z"

func (e *env) makeBackup() string {
	e.t.Helper()
	dir := filepath.Join(e.dstP, backupName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	content := map[string]string{"a.txt": "alpha", "b.txt": "bravo"}
	entries := []manifest.Entry{{Path: ".", Type: manifest.TypeDir, Mode: 0o755}}
	for n, c := range content {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			e.t.Fatal(err)
		}
		h := sha256.Sum256([]byte(c))
		entries = append(entries, manifest.Entry{Path: n, Type: manifest.TypeFile, Size: int64(len(c)), Mode: 0o644, SHA256: hex.EncodeToString(h[:])})
	}
	var tsv bytes.Buffer
	if err := manifest.WriteTSV(&tsv, entries); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.TSVName), tsv.Bytes(), 0o644); err != nil {
		e.t.Fatal(err)
	}
	h := sha256.Sum256(tsv.Bytes())
	m := backupset.Marker{Job: "data", Created: time.Now(), ManifestSHA256: hex.EncodeToString(h[:])}
	if err := backupset.WriteMarker(dir, m); err != nil {
		e.t.Fatal(err)
	}
	return dir
}

func TestVerifyByPathAndByID(t *testing.T) {
	e := newEnv(t)
	e.addJob()
	dir := e.makeBackup()
	e.mustRun("verify", dir)
	if !strings.Contains(e.out.String(), "ok: "+dir+" (2 of 2 file(s) re-hashed)") {
		t.Fatalf("%q", e.out.String())
	}
	e.mustRun("verify", "data/data-20260101T000000Z", "--sample", "50")
	if !strings.Contains(e.out.String(), "(1 of 2 file(s) re-hashed)") {
		t.Fatalf("%q", e.out.String())
	}
}

func TestVerifyFailuresAndWarnings(t *testing.T) {
	e := newEnv(t)
	e.addJob()
	dir := e.makeBackup()
	if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.mustRun("verify", dir)
	if !strings.Contains(e.out.String(), "warning: not in manifest: extra.txt") {
		t.Fatalf("%q", e.out.String())
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alphX"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Failed, "verify", dir)
	for _, want := range []string{"FAILED: a.txt: checksum mismatch", "FAILED: b.txt: no such file or directory"} {
		if !strings.Contains(e.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, e.out.String())
		}
	}
}

func TestVerifyUsageErrors(t *testing.T) {
	e := newEnv(t)
	e.addJob()
	dir := e.makeBackup()
	for _, pct := range []string{"0", "-1", "100.1", "abc"} {
		e.expectCode(exitcode.Usage, "verify", "--sample", pct, dir)
	}
	e.expectCode(exitcode.Usage, "verify")
	e.expectCode(exitcode.Usage, "verify", "nowhere")
	e.expectCode(exitcode.Usage, "verify", "nojob/name")
	e.expectCode(exitcode.Usage, "verify", "data/missing-backup")
	e.expectCode(exitcode.Usage, "verify", "data/../etc")
	e.expectCode(exitcode.Usage, "verify", "data/")
}

func TestResolveBackupVariants(t *testing.T) {
	e := newEnv(t)
	e.addJob()
	dir := e.makeBackup()
	a := &app{ctx: t.Context(), d: e.deps, stateDir: e.state}
	for _, arg := range []string{dir, dir + "/", "data/data-20260101T000000Z"} {
		got, err := a.resolveBackup(arg)
		if err != nil || got != dir {
			t.Errorf("%q -> %q (%v)", arg, got, err)
		}
	}
}
