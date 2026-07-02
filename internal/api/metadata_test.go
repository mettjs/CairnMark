package api

import (
	"net/url"
	"testing"
)

func TestClampLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"default when unset", 0, listDefaultLimit},
		{"negative -> default", -5, listDefaultLimit},
		{"over max -> capped", 10000, listMaxLimit},
		{"in range kept", 25, 25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampLimit(tt.limit); got != tt.want {
				t.Fatalf("clampLimit(%d) = %d, want %d", tt.limit, got, tt.want)
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
