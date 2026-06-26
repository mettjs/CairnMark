package files

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
)

// verifyingReader streams an object while hashing it, comparing the result to
// the recorded checksum once the underlying reader reaches EOF. A mismatch is
// surfaced as ErrChecksumMismatch in place of io.EOF, so a caller doing
// io.Copy observes the corruption (the bytes are still delivered — detection is
// the contract here, not prevention; presigned downloads bypass it entirely).
type verifyingReader struct {
	rc      io.ReadCloser
	h       hash.Hash
	want    string
	checked bool
}

// newVerifyingReader wraps rc to verify against want (hex SHA-256). If want is
// empty (a pre-Phase-2 record with no checksum) rc is returned unwrapped.
func newVerifyingReader(rc io.ReadCloser, want string) io.ReadCloser {
	if want == "" {
		return rc
	}
	return &verifyingReader{rc: rc, h: sha256.New(), want: want}
}

func (v *verifyingReader) Read(p []byte) (int, error) {
	n, err := v.rc.Read(p)
	if n > 0 {
		v.h.Write(p[:n])
	}
	if err == io.EOF && !v.checked {
		v.checked = true
		if got := hex.EncodeToString(v.h.Sum(nil)); got != v.want {
			return n, ErrChecksumMismatch
		}
	}
	return n, err
}

func (v *verifyingReader) Close() error { return v.rc.Close() }
