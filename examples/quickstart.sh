#!/usr/bin/env bash
# End-to-end demo against a running CairnMark (default http://localhost:8080).
# Start the stack first:  docker compose up -d --build
set -euo pipefail

BASE="${CAIRNMARK_BASE:-http://localhost:8080}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "1) Upload a file with tags"
echo "the cairn marks the path" > "$TMP/note.txt"
RESP=$(curl -fsS -H "Content-Type: text/plain" \
  --data-binary @"$TMP/note.txt" \
  "$BASE/files?filename=note.txt&tag.project=cairnmark&tag.env=demo")
echo "$RESP"
ID=$(printf '%s' "$RESP" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
echo "   -> id=$ID"

echo
echo "2) Fetch its metadata"
curl -fsS "$BASE/files/$ID/metadata"; echo

echo
echo "3) Download it (stream mode; default GET 302-redirects to a presigned URL)"
curl -fsS "$BASE/files/$ID?download=stream"

echo
echo "4) Search by tag — find every file tagged env=demo"
curl -fsS "$BASE/files?tag.env=demo"; echo

echo
echo "5) Add a tag, then delete"
curl -fsS -X PATCH -d '{"reviewed":true}' "$BASE/files/$ID/metadata"; echo
curl -fsS -o /dev/null -w "   delete -> HTTP %{http_code}\n" -X DELETE "$BASE/files/$ID"
