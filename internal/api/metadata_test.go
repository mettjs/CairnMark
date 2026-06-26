package api

import (
	"net/url"
	"testing"
)

func TestClampPage(t *testing.T) {
	tests := []struct {
		name                  string
		limit, offset         int
		wantLimit, wantOffset int
	}{
		{"defaults", 0, 0, listDefaultLimit, 0},
		{"negative limit -> default", -5, 0, listDefaultLimit, 0},
		{"over max -> capped", 10000, 0, listMaxLimit, 0},
		{"in range kept", 25, 10, 25, 10},
		{"negative offset -> zero", 25, -3, 25, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, o := clampPage(tt.limit, tt.offset)
			if l != tt.wantLimit || o != tt.wantOffset {
				t.Fatalf("clampPage(%d,%d) = (%d,%d), want (%d,%d)",
					tt.limit, tt.offset, l, o, tt.wantLimit, tt.wantOffset)
			}
		})
	}
}

func TestTagParams(t *testing.T) {
	q := url.Values{
		"tag.env":      {"prod"},
		"tag.team":     {"search"},
		"tag.":         {"ignored"}, // empty key after prefix
		"content_type": {"text/plain"},
		"limit":        {"10"},
	}
	tags := tagParams(q)
	if len(tags) != 2 || tags["env"] != "prod" || tags["team"] != "search" {
		t.Fatalf("tagParams extracted %v", tags)
	}
}
