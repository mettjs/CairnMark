package api

import "testing"

func TestParseSingleRange(t *testing.T) {
	const size = 100
	tests := []struct {
		name        string
		header      string
		wantOffset  int64
		wantLength  int64
		wantOutcome rangeOutcome
	}{
		{"closed", "bytes=0-9", 0, 10, rangeOK},
		{"closed-mid", "bytes=10-19", 10, 10, rangeOK},
		{"open-ended", "bytes=90-", 90, 10, rangeOK},
		{"suffix", "bytes=-10", 90, 10, rangeOK},
		{"suffix-larger-than-size", "bytes=-500", 0, 100, rangeOK},
		{"end-clamped-to-size", "bytes=95-200", 95, 5, rangeOK},
		{"first-of-multiple", "bytes=0-9,20-29", 0, 10, rangeOK},
		// Invalid syntax or a foreign unit → ignore the header (RFC 9110 §14.2).
		{"no-prefix", "0-9", 0, 0, rangeIgnore},
		{"other-unit", "items=0-9", 0, 0, rangeIgnore},
		{"no-dash", "bytes=10", 0, 0, rangeIgnore},
		{"reversed", "bytes=50-10", 0, 0, rangeIgnore},
		{"garbage", "bytes=abc-def", 0, 0, rangeIgnore},
		{"negative-suffix", "bytes=--5", 0, 0, rangeIgnore},
		// Valid syntax that names no bytes of the object → 416.
		{"start-beyond-size", "bytes=100-110", 0, 0, rangeUnsatisfiable},
		{"open-ended-beyond-size", "bytes=100-", 0, 0, rangeUnsatisfiable},
		{"zero-suffix", "bytes=-0", 0, 0, rangeUnsatisfiable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			off, length, outcome := parseSingleRange(tt.header, size)
			if outcome != tt.wantOutcome {
				t.Fatalf("outcome: got %v want %v", outcome, tt.wantOutcome)
			}
			if outcome != rangeOK {
				return
			}
			if off != tt.wantOffset || length != tt.wantLength {
				t.Fatalf("got offset=%d length=%d want offset=%d length=%d",
					off, length, tt.wantOffset, tt.wantLength)
			}
		})
	}
}

func TestParseSingleRangeEmptyObject(t *testing.T) {
	// No range is satisfiable against a zero-length object.
	for _, header := range []string{"bytes=0-", "bytes=0-9", "bytes=-10"} {
		if _, _, outcome := parseSingleRange(header, 0); outcome != rangeUnsatisfiable {
			t.Fatalf("%s on empty object: got %v want rangeUnsatisfiable", header, outcome)
		}
	}
}
