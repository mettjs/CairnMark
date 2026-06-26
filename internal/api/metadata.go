package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mettjs/cairnmark/internal/files"
)

// tagPrefix marks query parameters that become metadata tags / filters, e.g.
// ?tag.env=prod → {"env": "prod"}.
const tagPrefix = "tag."

// List pagination bounds applied at the edge so the response echoes the limit
// that was actually used (the repository enforces the same bounds defensively).
const (
	listDefaultLimit = 50
	listMaxLimit     = 500
)

// uploadTags builds the metadata to attach on upload from two sources, merged
// with query params taking precedence:
//   - an X-Metadata header carrying a JSON object (typed/nested values);
//   - tag.<key>=<value> query params (string values, curl-friendly).
func uploadTags(r *http.Request) (map[string]any, error) {
	tags := map[string]any{}
	if h := r.Header.Get("X-Metadata"); h != "" {
		if err := json.Unmarshal([]byte(h), &tags); err != nil {
			return nil, fmt.Errorf("X-Metadata must be a JSON object: %v", err)
		}
	}
	for k, v := range tagParams(r.URL.Query()) {
		tags[k] = v
	}
	if len(tags) == 0 {
		return nil, nil
	}
	return tags, nil
}

// tagParams extracts tag.<key>=<value> pairs as string-valued tags.
func tagParams(q url.Values) map[string]any {
	tags := map[string]any{}
	for key, vals := range q {
		if name, ok := strings.CutPrefix(key, tagPrefix); ok && name != "" && len(vals) > 0 {
			tags[name] = vals[0]
		}
	}
	return tags
}

// patchMetadata merges (default) or replaces (?mode=replace) the JSONB tags of
// a file. The body is a JSON object.
func (h *fileHandler) patchMetadata(w http.ResponseWriter, r *http.Request) {
	var tags map[string]any
	if err := json.NewDecoder(r.Body).Decode(&tags); err != nil {
		writeClientError(w, http.StatusBadRequest, "body must be a JSON object: "+err.Error())
		return
	}
	if tags == nil { // body was literal `null` — not a usable tag set
		writeClientError(w, http.StatusBadRequest, "body must be a JSON object, not null")
		return
	}
	merge := r.URL.Query().Get("mode") != "replace"

	f, err := h.svc.UpdateMetadata(r.Context(), r.PathValue("id"), tags, merge)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toResponse(f))
}

// list returns files filtered by content_type and tag.<key> params, newest
// first, with limit/offset pagination.
func (h *fileHandler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := atoiDefault(q.Get("limit"), 0)
	if err != nil {
		writeClientError(w, http.StatusBadRequest, "limit must be an integer")
		return
	}
	offset, err := atoiDefault(q.Get("offset"), 0)
	if err != nil {
		writeClientError(w, http.StatusBadRequest, "offset must be an integer")
		return
	}
	limit, offset = clampPage(limit, offset)

	results, err := h.svc.List(r.Context(), files.ListFilter{
		ContentType: q.Get("content_type"),
		Tags:        tagParams(q),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		h.writeError(w, err)
		return
	}

	items := make([]fileResponse, 0, len(results))
	for _, f := range results {
		items = append(items, toResponse(f))
	}
	writeJSON(w, http.StatusOK, listResponse{Files: items, Limit: limit, Offset: offset, Count: len(items)})
}

type listResponse struct {
	Files  []fileResponse `json:"files"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
	Count  int            `json:"count"`
}

func atoiDefault(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	return strconv.Atoi(s)
}

// clampPage normalizes limit/offset so the values returned in the response are
// exactly what was applied to the query.
func clampPage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = listDefaultLimit
	}
	if limit > listMaxLimit {
		limit = listMaxLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
