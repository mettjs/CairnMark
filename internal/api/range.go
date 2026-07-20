package api

import (
	"strconv"
	"strings"
)

// rangeOutcome classifies a Range header per RFC 9110 §14: a syntactically
// invalid header (or one with a unit other than bytes) MUST be ignored — the
// request is served as a normal GET — while a well-formed range that doesn't
// overlap the object gets a 416.
type rangeOutcome int

const (
	rangeIgnore        rangeOutcome = iota // malformed or non-bytes unit; serve the full object
	rangeUnsatisfiable                     // valid syntax, no satisfiable range; respond 416
	rangeOK                                // serve [offset, offset+length)
)

// parseSingleRange parses an HTTP Range header against the object size and
// returns the byte offset and length to serve. Only a single range is
// supported; if multiple are given, the first is used.
//
// Forms handled: "bytes=start-end", "bytes=start-" (start..EOF), and
// "bytes=-suffix" (final suffix bytes).
func parseSingleRange(header string, size int64) (offset, length int64, outcome rangeOutcome) {
	const prefix = "bytes="
	if !strings.HasPrefix(header, prefix) {
		return 0, 0, rangeIgnore
	}
	spec := strings.TrimPrefix(header, prefix)
	if i := strings.IndexByte(spec, ','); i >= 0 {
		spec = spec[:i] // first range only
	}
	spec = strings.TrimSpace(spec)

	startStr, endStr, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, rangeIgnore
	}

	// Suffix form: "-N" → last N bytes. "-0" is valid syntax but names an
	// empty range — unsatisfiable, not ignorable.
	if startStr == "" {
		n, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || n < 0 { // a negative suffix is invalid syntax, not an empty range
			return 0, 0, rangeIgnore
		}
		if n == 0 || size == 0 {
			return 0, 0, rangeUnsatisfiable
		}
		if n > size {
			n = size
		}
		return size - n, n, rangeOK
	}

	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return 0, 0, rangeIgnore
	}

	// Open-ended form: "start-" → start..EOF.
	if endStr == "" {
		if start >= size {
			return 0, 0, rangeUnsatisfiable
		}
		return start, size - start, rangeOK
	}

	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < start {
		return 0, 0, rangeIgnore // reversed bounds are invalid syntax, not unsatisfiable
	}
	if start >= size {
		return 0, 0, rangeUnsatisfiable
	}
	if end >= size {
		end = size - 1
	}
	return start, end - start + 1, rangeOK
}
