package api

import (
	"errors"
	"io"
)

// errUploadTooLarge fails the read that would take an upload body past the
// configured cap.
var errUploadTooLarge = errors.New("api: upload body exceeds the size limit")

// limitReader passes through at most max bytes of an upload body and fails the
// read that would go beyond them. It records the breach in exceeded so the
// handler can answer 413 no matter how the layers between (service, minio-go)
// wrap or replace the read error on its way back up.
type limitReader struct {
	r         io.Reader
	remaining int64
	exceeded  bool
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.exceeded {
		return 0, errUploadTooLarge
	}
	if l.remaining <= 0 {
		// At the cap: probe one byte to tell "body ended exactly here" (EOF)
		// from "there was more" (too large).
		var b [1]byte
		n, err := l.r.Read(b[:])
		if n > 0 {
			l.exceeded = true
			return 0, errUploadTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.r.Read(p)
	l.remaining -= int64(n)
	return n, err
}
