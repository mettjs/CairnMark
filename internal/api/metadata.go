package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
	maps.Copy(tags, tagParams(r.URL.Query()))
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

// maxMetadataBodyBytes caps a PATCH metadata body. Tags are small; without a
// cap the decoded map grows with whatever the client streams.
const maxMetadataBodyBytes = 1 << 20 // 1 MiB

// patchMetadata merges (default) or replaces (?mode=replace) the JSONB tags of
// a file. The body is a JSON object.
func (h *fileHandler) patchMetadata(w http.ResponseWriter, r *http.Request) {
	var tags map[string]any
	body := http.MaxBytesReader(w, r.Body, maxMetadataBodyBytes)
	if err := json.NewDecoder(body).Decode(&tags); err != nil {
		if tooLarge, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeClientError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("metadata body exceeds the %d-byte limit", tooLarge.Limit))
			return
		}
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
// first, with keyset pagination: pass a page's next_cursor back as ?cursor= to
// fetch the files older than it.
func (h *fileHandler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := atoiDefault(q.Get("limit"), 0)
	if err != nil {
		writeClientError(w, http.StatusBadRequest, "limit must be an integer")
		return
	}
	limit = clampLimit(limit)

	results, err := h.svc.List(r.Context(), files.ListFilter{
		ContentType: q.Get("content_type"),
		Tags:        tagParams(q),
		Limit:       limit,
		Cursor:      q.Get("cursor"),
	})
	if err != nil {
		h.writeError(w, err)
		return
	}

	items := make([]fileResponse, 0, len(results))
	for _, f := range results {
		items = append(items, toResponse(f))
	}
	resp := listResponse{Files: items, Limit: limit, Count: len(items)}
	// A full page may have more behind it; a short page is definitely the last.
	// When the total is an exact multiple of limit, the final cursor yields one
	// empty page — the unambiguous end-of-list signal either way is no cursor.
	if len(items) == limit {
		resp.NextCursor = items[len(items)-1].ID
	}
	writeJSON(w, http.StatusOK, resp)
}

type listResponse struct {
	Files      []fileResponse `json:"files"`
	Limit      int            `json:"limit"`
	Count      int            `json:"count"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

func atoiDefault(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	return strconv.Atoi(s)
}

// clampLimit normalizes the page size so the value returned in the response is
// exactly what was applied to the query.
func clampLimit(limit int) int {
	if limit <= 0 {
		return listDefaultLimit
	}
	if limit > listMaxLimit {
		return listMaxLimit
	}
	return limit
}
