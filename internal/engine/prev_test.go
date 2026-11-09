package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/manifest"
	"github.com/drizki/backup-cli/internal/runlog"
)

func TestPreviousManifestWithAnInvalidChecksumFallsBackToSource(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	dir := f.latest()
	tsv := filepath.Join(dir, manifest.TSVName)
	raw, err := os.ReadFile(tsv)
	if err != nil {
		t.Fatal(err)
	}
	var sum string
	for _, line := range strings.Split(string(raw), "\n") {
		if cols := strings.Split(line, "\t"); len(cols) == 9 && cols[1] == manifest.TypeFile {
			sum = cols[7]
			break
		}
	}
	bad := strings.Replace(string(raw), sum, strings.Repeat("z", 64), 1)
	if sum == "" || bad == string(raw) {
		t.Fatalf("no file row to damage:\n%s", raw)
	}
	if err := os.WriteFile(tsv, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := backupset.ReadMarker(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(bad))
	m.ManifestSHA256 = hex.EncodeToString(h[:])
	if err := backupset.WriteMarker(dir, m); err != nil {
		t.Fatal(err)
	}
	f.mustRun()
	if st := f.lastStatus(); !hasWarning(st, "reading everything from the source") || st.FilesCopiedFromSource != 6 {
		t.Fatalf("%+v", st)
	}
}

func TestPreviousEntriesKeepOnlyWhatARunNeeds(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	r := &run{e: f.e, j: f.j, log: mirrorLog(f.out)}
	var err error
	if r.backups, err = backupset.Scan(f.j); err != nil {
		t.Fatal(err)
	}
	prev := r.loadPrev()
	if prev == nil {
		t.Fatalf("no previous backup loaded:\n%s", f.out)
	}
	files, dirs := 0, 0
	for _, pe := range prev.entries {
		if pe.file {
			files++
			if pe.sumHex() == strings.Repeat("0", 64) {
				t.Error("a file has an empty checksum")
			}
			continue
		}
		dirs++
	}
	if files != 6 || dirs == 0 {
		t.Fatalf("files %d dirs %d", files, dirs)
	}
	if _, ok := prev.lookup("no/such/path"); ok {
		t.Fatal("found a path that is not in the backup")
	}
}

func mirrorLog(w *bytes.Buffer) *runlog.Log { return runlog.Mirror(w) }
