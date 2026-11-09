package copyx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func hexSum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestCopyHashAndSize(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 70000) // > one buffer
	var out bytes.Buffer
	n, sum, err := Copy(context.Background(), &out, bytes.NewReader(data), make([]byte, 4096))
	if err != nil || n != int64(len(data)) || sum != hexSum(data) || !bytes.Equal(out.Bytes(), data) {
		t.Fatalf("n=%d sum=%s err=%v", n, sum, err)
	}
	n, sum, err = Copy(context.Background(), io.Discard, strings.NewReader(""), make([]byte, 8))
	if err != nil || n != 0 || sum != hexSum(nil) {
		t.Fatalf("empty: %d %s %v", n, sum, err)
	}
}

func TestCopyMatchesSha256sum(t *testing.T) {
	bin, err := exec.LookPath("sha256sum")
	if err != nil {
		t.Skip("sha256sum binary missing")
	}
	p := writeTemp(t, bytes.Repeat([]byte("xyz"), 500000))
	_, sum, err := Hash(context.Background(), mustOpen(t, p), make([]byte, BufSize))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, p).Output()
	if err != nil || !strings.HasPrefix(string(out), sum+" ") {
		t.Fatalf("sha256sum says %q, we say %s (%v)", out, sum, err)
	}
}

func mustOpen(t *testing.T, p string) *os.File {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// trap implements the fast-path interfaces and fails the test if they run.
type trapReader struct {
	io.Reader
	t *testing.T
}

func (r trapReader) WriteTo(io.Writer) (int64, error) {
	r.t.Error("WriterTo fast path was used")
	return 0, errors.New("fast path")
}

type trapWriter struct {
	io.Writer
	t *testing.T
}

func (w trapWriter) ReadFrom(io.Reader) (int64, error) {
	w.t.Error("ReaderFrom fast path was used")
	return 0, errors.New("fast path")
}

func TestCopyNeverUsesFastPaths(t *testing.T) {
	var out bytes.Buffer
	src := trapReader{Reader: strings.NewReader("hello fast paths"), t: t}
	if _, _, err := Copy(context.Background(), trapWriter{Writer: &out, t: t}, src, make([]byte, 4)); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello fast paths" {
		t.Fatalf("got %q", out.String())
	}
}

// TestWrappersHideFastPaths proves the wrapper types given to the loop expose
// only Read/Write even though *os.File offers ReadFrom and WriteTo.
func TestWrappersHideFastPaths(t *testing.T) {
	var file any = &os.File{}
	if _, ok := file.(io.ReaderFrom); !ok {
		t.Fatal("precondition: *os.File should implement io.ReaderFrom")
	}
	if _, ok := file.(io.WriterTo); !ok {
		t.Fatal("precondition: *os.File should implement io.WriterTo")
	}
	var r any = readOnly{r: &os.File{}}
	var w any = writeOnly{w: &os.File{}}
	if _, ok := r.(io.WriterTo); ok {
		t.Error("readOnly exposes WriteTo")
	}
	if _, ok := r.(io.ReaderFrom); ok {
		t.Error("readOnly exposes ReadFrom")
	}
	if _, ok := w.(io.ReaderFrom); ok {
		t.Error("writeOnly exposes ReadFrom")
	}
	if _, ok := w.(io.WriterTo); ok {
		t.Error("writeOnly exposes WriteTo")
	}
}

type failWriter struct{ n int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.n++; f.n > 1 {
		return 0, errors.New("disk full")
	}
	return len(p) / 2, nil // short write on the first call
}

func TestCopyErrors(t *testing.T) {
	_, _, err := Copy(context.Background(), &failWriter{}, strings.NewReader("abcdefgh"), make([]byte, 8))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write: %v", err)
	}
	_, _, err = Copy(context.Background(), &failWriter{n: 5}, strings.NewReader("abc"), make([]byte, 8))
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("write error: %v", err)
	}
	_, _, err = Copy(context.Background(), io.Discard, iotestErrReader{}, make([]byte, 8))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("read error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Copy(ctx, io.Discard, strings.NewReader("abc"), make([]byte, 8)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

type iotestErrReader struct{}

func (iotestErrReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestBufferPoolReuses(t *testing.T) {
	b := GetBuf()
	if len(*b) != BufSize {
		t.Fatalf("buffer size %d", len(*b))
	}
	PutBuf(b)
	if c := GetBuf(); len(*c) != BufSize {
		t.Fatalf("buffer size %d", len(*c))
	}
}

func TestHashFile(t *testing.T) {
	p := writeTemp(t, []byte("hash me"))
	n, sum, err := HashFile(context.Background(), p, make([]byte, 16))
	if err != nil || n != 7 || sum != hexSum([]byte("hash me")) {
		t.Fatalf("%d %s %v", n, sum, err)
	}
	link := filepath.Join(t.TempDir(), "l")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := HashFile(context.Background(), link, make([]byte, 16)); err == nil {
		t.Fatal("HashFile followed a symlink")
	}
	if _, _, err := HashFile(context.Background(), p+"-missing", make([]byte, 16)); err == nil {
		t.Fatal("missing file hashed")
	}
}

func TestOpenReadOnlyFlagsAndFallback(t *testing.T) {
	var seen []int
	open := func(flags int) (*os.File, error) {
		seen = append(seen, flags)
		return nil, syscall.EPERM
	}
	if _, err := OpenReadOnly(open); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("err %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("expected a retry, saw %d opens", len(seen))
	}
	want := os.O_RDONLY | syscall.O_NOFOLLOW
	if seen[0] != want|syscall.O_NOATIME || seen[1] != want {
		t.Fatalf("flags %#x %#x", seen[0], seen[1])
	}
	if seen[0]&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_CREAT|syscall.O_TRUNC) != 0 {
		t.Fatal("source open requests write access")
	}
	seen = nil
	other := errors.New("other")
	if _, err := OpenReadOnly(func(f int) (*os.File, error) { seen = append(seen, f); return nil, other }); !errors.Is(err, other) || len(seen) != 1 {
		t.Fatalf("non-EPERM error must not retry: %v %d", err, len(seen))
	}
}

func TestOpenersReadFiles(t *testing.T) {
	p := writeTemp(t, []byte("abc"))
	for name, op := range map[string]FlagOpener{"path": PathOpener(p), "root": rootOpener(t, p)} {
		f, err := OpenReadOnly(op)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, _ := io.ReadAll(f)
		_ = f.Close()
		if string(b) != "abc" {
			t.Errorf("%s read %q", name, b)
		}
	}
}

func rootOpener(t *testing.T, p string) FlagOpener {
	t.Helper()
	root, err := os.OpenRoot(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return RootOpener(root, filepath.Base(p))
}

func TestOpenReadOnlyRefusesFinalSymlink(t *testing.T) {
	p := writeTemp(t, []byte("secret"))
	link := filepath.Join(t.TempDir(), "l")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if f, err := OpenReadOnly(PathOpener(link)); err == nil {
		_ = f.Close()
		t.Fatal("followed a symlink")
	}
}

func TestIsRoot(t *testing.T) {
	if IsRoot() != (os.Geteuid() == 0) {
		t.Fatal("IsRoot disagrees with euid")
	}
}
