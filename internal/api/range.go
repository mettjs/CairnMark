package api

import (
	"strconv"
	"strings"
)

// parseSingleRange parses an HTTP Range header against the object size and
// returns the byte offset and length to serve. Only a single range is
// supported; if multiple are given, the first is used. ok is false when the
// header is malformed or the range cannot be satisfied (caller responds 416).
//
// Forms handled: "bytes=start-end", "bytes=start-" (start..EOF), and
// "bytes=-suffix" (final suffix bytes).
func parseSingleRange(header string, size int64) (offset, length int64, ok bool) {
	const prefix = "bytes="
	if !strings.HasPrefix(header, prefix) {
		return 0, 0, false
	}
	spec := strings.TrimPrefix(header, prefix)
	if i := strings.IndexByte(spec, ','); i >= 0 {
		spec = spec[:i] // first range only
	}
	spec = strings.TrimSpace(spec)

	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, 0, false
	}
	startStr, endStr := spec[:dash], spec[dash+1:]

	// Suffix form: "-N" → last N bytes.
	if startStr == "" {
		n, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		if n > size {
			n = size
		}
		return size - n, n, size > 0
	}

	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}

	// Open-ended form: "start-" → start..EOF.
	if endStr == "" {
		return start, size - start, true
	}

	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < start {
		return 0, 0, false
	}
	if end >= size {
		end = size - 1
	}
	return start, end - start + 1, true
}
