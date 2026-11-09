package manifest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/copyx"
)

// JournalName is the resume journal that may linger inside a committed
// backup; it is bookkeeping, not data.
const JournalName = ".backup-cli-partial.jsonl"

// VerifyOptions controls Verify.
type VerifyOptions struct {
	// SamplePct is the percentage of regular files to re-hash (0 < p <= 100;
	// at least one file is always checked). Directories and symlinks are
	// always checked.
	SamplePct float64
	// Rand selects the sample; nil seeds from the clock.
	Rand *rand.Rand
}

// Report is the result of a verification.
type Report struct {
	Files    int // regular files in the manifest
	Checked  int // regular files re-hashed
	Failures []string
	Warnings []string
}

// OK reports whether nothing failed.
func (r Report) OK() bool { return len(r.Failures) == 0 }

// SampleSize is how many of n files a percentage selects.
func SampleSize(n int, pct float64) int {
	if n == 0 {
		return 0
	}
	k := int(math.Ceil(float64(n) * pct / 100))
	return min(max(k, 1), n)
}

// Verify checks a committed backup against its manifest. The error return is
// reserved for conditions that stop verification (cancellation, unreadable
// directory); problems with the backup itself are Failures.
func Verify(ctx context.Context, dir string, o VerifyOptions) (Report, error) {
	var rep Report
	if o.SamplePct <= 0 || o.SamplePct > 100 {
		return rep, fmt.Errorf("sample percentage must be above 0 and at most 100, got %v", o.SamplePct)
	}
	entries, err := loadVerified(dir, &rep)
	if err != nil || len(entries) == 0 {
		return rep, err
	}
	rng := o.Rand
	if rng == nil {
		rng = rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 1))
	}
	files := checkTree(dir, entries, &rep)
	rep.Files = len(files)
	buf := copyx.GetBuf()
	defer copyx.PutBuf(buf)
	for _, i := range rng.Perm(len(files))[:SampleSize(len(files), o.SamplePct)] {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		checkFile(ctx, dir, files[i], *buf, &rep)
	}
	findExtras(dir, entries, &rep)
	return rep, ctx.Err()
}

// loadVerified reads the marker and manifest and checks their agreement.
// Failures are recorded in rep; the entries are empty when it cannot go on.
func loadVerified(dir string, rep *Report) ([]Entry, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}
	m, err := backupset.ReadMarker(dir)
	if err != nil {
		rep.Failures = append(rep.Failures, fmt.Sprintf("marker: %v", err))
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, TSVName))
	if err != nil {
		rep.Failures = append(rep.Failures, fmt.Sprintf("%s: %v", TSVName, err))
		return nil, nil
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != m.ManifestSHA256 {
		rep.Failures = append(rep.Failures, TSVName+" does not match the checksum in the marker")
		return nil, nil
	}
	entries, err := ParseTSV(bytes.NewReader(raw))
	if err != nil {
		rep.Failures = append(rep.Failures, fmt.Sprintf("%s: %v", TSVName, err))
		return nil, nil
	}
	return entries, nil
}

// checkTree verifies directories and symlinks and returns the file entries.
func checkTree(dir string, entries []Entry, rep *Report) []Entry {
	var files []Entry
	for _, e := range entries {
		full := filepath.Join(dir, filepath.FromSlash(e.Path))
		fi, err := os.Lstat(full)
		switch {
		case err != nil:
			rep.Failures = append(rep.Failures, fmt.Sprintf("%s: %v", e.Path, simplify(err)))
		case e.Type == TypeFile:
			files = append(files, e)
		case e.Type == TypeDir && !fi.IsDir():
			rep.Failures = append(rep.Failures, e.Path+": expected a directory")
		case e.Type == TypeSymlink:
			checkLink(full, e, rep)
		}
	}
	return files
}

func checkLink(full string, e Entry, rep *Report) {
	target, err := os.Readlink(full)
	if err != nil || target != e.LinkTarget {
		rep.Failures = append(rep.Failures, e.Path+": symlink target differs from the manifest")
	}
}

func checkFile(ctx context.Context, dir string, e Entry, buf []byte, rep *Report) {
	rep.Checked++
	full := filepath.Join(dir, filepath.FromSlash(e.Path))
	size, sum, err := copyx.HashFile(ctx, full, buf)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return // already reported by checkTree
	case err != nil:
		rep.Failures = append(rep.Failures, fmt.Sprintf("%s: %v", e.Path, simplify(err)))
	case size != e.Size:
		rep.Failures = append(rep.Failures, fmt.Sprintf("%s: size %d, manifest says %d", e.Path, size, e.Size))
	case sum != e.SHA256:
		rep.Failures = append(rep.Failures, e.Path+": checksum mismatch")
	}
}

func simplify(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// findExtras warns about anything in the tree the manifest does not list.
func findExtras(dir string, entries []Entry, rep *Report) {
	known := make(map[string]bool, len(entries))
	for _, e := range entries {
		known[e.Path] = true
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if known[rel] || isMeta(rel) {
			return nil
		}
		rep.Warnings = append(rep.Warnings, "not in manifest: "+rel)
		if d.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
}

func isMeta(rel string) bool {
	switch rel {
	case SHA256Name, TSVName, backupset.MarkerName, JournalName:
		return true
	}
	return strings.HasPrefix(rel, backupset.MarkerName+".tmp-")
}
