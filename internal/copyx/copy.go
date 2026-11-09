// Package copyx copies file data with an explicit buffer loop, hashes it in
// the same pass and applies metadata. It never uses kernel copy offload,
// reflinks or hardlinks: every byte is read and written by this package.
package copyx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sync"
)

// BufSize is the size of the pooled copy buffers.
const BufSize = 1 << 20

var pool = sync.Pool{New: func() any {
	b := make([]byte, BufSize)
	return &b
}}

// GetBuf returns a reusable copy buffer. Return it with PutBuf.
func GetBuf() *[]byte { return pool.Get().(*[]byte) }

// PutBuf gives a buffer back to the pool.
func PutBuf(b *[]byte) { pool.Put(b) }

// readOnly and writeOnly expose only Read and Write. Wrapping hides the
// ReaderFrom and WriterTo methods of *os.File, which would let the standard
// library hand the transfer to the kernel.
type readOnly struct{ r io.Reader }

func (r readOnly) Read(p []byte) (int, error) { return r.r.Read(p) }

type writeOnly struct{ w io.Writer }

func (w writeOnly) Write(p []byte) (int, error) { return w.w.Write(p) }

// Copy streams src to dst through buf and returns the byte count and the
// hex SHA-256 of the data, computed in the same pass. The context is checked
// between chunks.
func Copy(ctx context.Context, dst io.Writer, src io.Reader, buf []byte) (int64, string, error) {
	r, w := io.Reader(readOnly{src}), io.Writer(writeOnly{dst})
	h := sha256.New()
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, "", err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			wn, werr := w.Write(buf[:n])
			total += int64(wn)
			if werr != nil {
				return total, "", werr
			}
			if wn != n {
				return total, "", io.ErrShortWrite
			}
		}
		if rerr == io.EOF {
			return total, hex.EncodeToString(h.Sum(nil)), nil
		}
		if rerr != nil {
			return total, "", rerr
		}
	}
}

// Hash returns the size and hex SHA-256 of everything readable from r.
func Hash(ctx context.Context, r io.Reader, buf []byte) (int64, string, error) {
	return Copy(ctx, io.Discard, r, buf)
}

// HashFile hashes the regular file at path (symlinks are not followed).
func HashFile(ctx context.Context, path string, buf []byte) (int64, string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|noFollow, 0)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = f.Close() }()
	return Hash(ctx, f, buf)
}
