package manifest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
)

// buildBackup writes a committed backup with the given files and returns its
// directory and entries.
func buildBackup(t *testing.T, files map[string]string) (string, []Entry) {
	t.Helper()
	dir := t.TempDir()
	entries := []Entry{{Path: RootPath, Type: TypeDir, Mode: 0o755}}
	seen := map[string]bool{}
	for name := range files {
		for d := filepath.ToSlash(filepath.Dir(name)); d != "."; d = filepath.ToSlash(filepath.Dir(d)) {
			if !seen[d] {
				seen[d] = true
				entries = append(entries, Entry{Path: d, Type: TypeDir, Mode: 0o755})
			}
		}
	}
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, Entry{Path: name, Type: TypeFile, Size: int64(len(content)), Mode: 0o644, SHA256: sum(content)})
	}
	entries = append(entries, Entry{Path: "lnk", Type: TypeSymlink, LinkTarget: "a.txt"})
	if err := os.Symlink("a.txt", filepath.Join(dir, "lnk")); err != nil {
		t.Fatal(err)
	}
	var tsv bytes.Buffer
	if err := WriteTSV(&tsv, entries); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, TSVName), tsv.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(tsv.Bytes())
	err := backupset.WriteMarker(dir, backupset.Marker{Job: "j", Created: time.Now(), ManifestSHA256: hex.EncodeToString(h[:])})
	if err != nil {
		t.Fatal(err)
	}
	return dir, entries
}

func testFiles() map[string]string {
	return map[string]string{"a.txt": "alpha", "sub/b.txt": "bravo", "sub/deep/c.txt": "charlie"}
}

func verifyAll(t *testing.T, dir string) Report {
	t.Helper()
	rep, err := Verify(context.Background(), dir, VerifyOptions{SamplePct: 100})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func hasFailure(rep Report, sub string) bool {
	for _, f := range rep.Failures {
		if strings.Contains(f, sub) {
			return true
		}
	}
	return false
}

func TestVerifyPass(t *testing.T) {
	dir, _ := buildBackup(t, testFiles())
	rep := verifyAll(t, dir)
	if !rep.OK() || rep.Files != 3 || rep.Checked != 3 || len(rep.Warnings) != 0 {
		t.Fatalf("%+v", rep)
	}
}

func TestVerifyFlippedByte(t *testing.T) {
	dir, _ := buildBackup(t, testFiles())
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("bravX"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rep := verifyAll(t, dir); !hasFailure(rep, "sub/b.txt: checksum mismatch") {
		t.Fatalf("%+v", rep)
	}
}

func TestVerifySizeAndMissing(t *testing.T) {
	dir, _ := buildBackup(t, testFiles())
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha-longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "sub", "deep", "c.txt")); err != nil {
		t.Fatal(err)
	}
	rep := verifyAll(t, dir)
	if !hasFailure(rep, "a.txt: size 12") || !hasFailure(rep, "sub/deep/c.txt: no such file") {
		t.Fatalf("%+v", rep)
	}
}

func TestVerifyExtraFileWarns(t *testing.T) {
	dir, _ := buildBackup(t, testFiles())
	for _, n := range []string{"extra.txt", JournalName} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "newdir", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	rep := verifyAll(t, dir)
	if !rep.OK() {
		t.Fatalf("extras must not fail: %+v", rep)
	}
	got := strings.Join(rep.Warnings, "|")
	if !strings.Contains(got, "not in manifest: extra.txt") || !strings.Contains(got, "not in manifest: newdir") || strings.Contains(got, "partial") {
		t.Fatalf("warnings: %v", rep.Warnings)
	}
}

func TestVerifySymlinkAndDirChecks(t *testing.T) {
	dir, _ := buildBackup(t, testFiles())
	if err := os.Remove(filepath.Join(dir, "lnk")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("elsewhere", filepath.Join(dir, "lnk")); err != nil {
		t.Fatal(err)
	}
	if rep := verifyAll(t, dir); !hasFailure(rep, "lnk: symlink target differs") {
		t.Fatalf("%+v", rep)
	}
	dir2, entries := buildBackup(t, testFiles())
	_ = entries
	if err := os.RemoveAll(filepath.Join(dir2, "sub")); err != nil {
		t.Fatal(err)
	}
	if rep := verifyAll(t, dir2); !hasFailure(rep, "sub/b.txt") {
		t.Fatalf("%+v", rep)
	}
}

func TestVerifyManifestProblems(t *testing.T) {
	dir, _ := buildBackup(t, testFiles())
	tsv := filepath.Join(dir, TSVName)
	raw, _ := os.ReadFile(tsv)
	if err := os.WriteFile(tsv, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if rep := verifyAll(t, dir); !hasFailure(rep, "does not match the checksum in the marker") {
		t.Fatalf("%+v", rep)
	}
	if err := os.Remove(tsv); err != nil {
		t.Fatal(err)
	}
	if rep := verifyAll(t, dir); !hasFailure(rep, TSVName) {
		t.Fatalf("%+v", rep)
	}
	if err := os.Remove(filepath.Join(dir, backupset.MarkerName)); err != nil {
		t.Fatal(err)
	}
	if rep := verifyAll(t, dir); !hasFailure(rep, "marker") {
		t.Fatalf("%+v", rep)
	}
}

func TestVerifyGarbageManifestWithMatchingMarker(t *testing.T) {
	dir, _ := buildBackup(t, testFiles())
	garbage := []byte("not a manifest\n")
	if err := os.WriteFile(filepath.Join(dir, TSVName), garbage, 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(garbage)
	if err := backupset.WriteMarker(dir, backupset.Marker{Job: "j", ManifestSHA256: hex.EncodeToString(h[:])}); err != nil {
		t.Fatal(err)
	}
	if rep := verifyAll(t, dir); !hasFailure(rep, "header") {
		t.Fatalf("%+v", rep)
	}
}

func TestVerifyPercentBounds(t *testing.T) {
	dir, _ := buildBackup(t, testFiles())
	for _, pct := range []float64{0, -1, 100.5, 1000} {
		if _, err := Verify(context.Background(), dir, VerifyOptions{SamplePct: pct}); err == nil {
			t.Errorf("pct %v accepted", pct)
		}
	}
	for _, pct := range []float64{0.001, 50, 100} {
		if _, err := Verify(context.Background(), dir, VerifyOptions{SamplePct: pct}); err != nil {
			t.Errorf("pct %v rejected: %v", pct, err)
		}
	}
	if _, err := Verify(context.Background(), filepath.Join(dir, "missing"), VerifyOptions{SamplePct: 5}); err == nil {
		t.Error("missing directory accepted")
	}
}

func TestSampleSize(t *testing.T) {
	cases := []struct {
		n    int
		pct  float64
		want int
	}{{0, 50, 0}, {1, 0.1, 1}, {10, 2, 1}, {100, 2, 2}, {101, 2, 3}, {10, 100, 10}, {7, 50, 4}}
	for _, c := range cases {
		if got := SampleSize(c.n, c.pct); got != c.want {
			t.Errorf("SampleSize(%d, %v) = %d, want %d", c.n, c.pct, got, c.want)
		}
	}
}

func TestVerifySamplesSubsetAndFindsDamage(t *testing.T) {
	files := map[string]string{}
	for i := range 40 {
		files[string(rune('a'+i%26))+strings.Repeat("x", i)] = strings.Repeat("v", i+1)
	}
	dir, entries := buildBackup(t, files)
	rep, err := Verify(context.Background(), dir, VerifyOptions{SamplePct: 10, Rand: rand.New(rand.NewPCG(1, 2))})
	if err != nil || rep.Checked != SampleSize(rep.Files, 10) || rep.Checked >= rep.Files {
		t.Fatalf("%+v %v", rep, err)
	}
	for _, e := range entries {
		if e.Type == TypeFile {
			if err := os.WriteFile(filepath.Join(dir, e.Path), []byte("damaged"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	rep, _ = Verify(context.Background(), dir, VerifyOptions{SamplePct: 5})
	if rep.OK() || rep.Checked == 0 {
		t.Fatalf("damage not found: %+v", rep)
	}
}

func TestVerifyCancelled(t *testing.T) {
	dir, _ := buildBackup(t, testFiles())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Verify(ctx, dir, VerifyOptions{SamplePct: 100}); err == nil {
		t.Fatal("cancelled verify returned nil")
	}
}
