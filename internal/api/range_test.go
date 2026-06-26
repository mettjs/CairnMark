package api

import "testing"

func TestParseSingleRange(t *testing.T) {
	const size = 100
	tests := []struct {
		name       string
		header     string
		wantOffset int64
		wantLength int64
		wantOK     bool
	}{
		{"closed", "bytes=0-9", 0, 10, true},
		{"closed-mid", "bytes=10-19", 10, 10, true},
		{"open-ended", "bytes=90-", 90, 10, true},
		{"suffix", "bytes=-10", 90, 10, true},
		{"suffix-larger-than-size", "bytes=-500", 0, 100, true},
		{"end-clamped-to-size", "bytes=95-200", 95, 5, true},
		{"first-of-multiple", "bytes=0-9,20-29", 0, 10, true},
		{"no-prefix", "0-9", 0, 0, false},
		{"no-dash", "bytes=10", 0, 0, false},
		{"start-beyond-size", "bytes=100-110", 0, 0, false},
		{"reversed", "bytes=50-10", 0, 0, false},
		{"garbage", "bytes=abc-def", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			off, length, ok := parseSingleRange(tt.header, size)
			if ok != tt.wantOK {
				t.Fatalf("ok: got %v want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if off != tt.wantOffset || length != tt.wantLength {
				t.Fatalf("got offset=%d length=%d want offset=%d length=%d",
					off, length, tt.wantOffset, tt.wantLength)
			}
		})
	}
}
