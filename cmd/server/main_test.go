package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ontology/ontology"
)

func newTestServer(t *testing.T) *server {
	t.Helper()
	audit := ontology.NewMemoryLogger(256)
	s := &server{
		engine: ontology.NewEngine(ontology.WithAuditLogger(audit)),
		audit:  audit,
	}
	if err := seed(s.engine); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return s
}

func get(t *testing.T, s *server, path string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func post(t *testing.T, s *server, path, payload string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(payload)))
	return rec.Code
}

func TestServerSmoke(t *testing.T) {
	s := newTestServer(t)

	// bob (admin) may read the confidential document inherited from folder-1.
	code, body := get(t, s, "/authorize?subject=bob&instance=doc-1&action=read")
	if code != http.StatusOK || body["allowed"] != true {
		t.Fatalf("bob: code=%d body=%v", code, body)
	}
	// alice (reader) has no grant for the inherited confidential tag.
	code, body = get(t, s, "/authorize?subject=alice&instance=doc-1&action=read")
	if code != http.StatusOK || body["allowed"] != false || body["reason"] != "missing-grant" {
		t.Fatalf("alice: code=%d body=%v", code, body)
	}
	// doc-1 inherits confidential from Folder via the contains link.
	code, body = get(t, s, "/instance/tags?id=doc-1")
	if code != http.StatusOK {
		t.Fatalf("tags: code=%d body=%v", code, body)
	}
	// Grant the reader role; alice becomes authorized.
	if code := post(t, s, "/grant", `{"role":"reader","tag":"confidential","effect":"allow"}`); code != http.StatusOK {
		t.Fatalf("grant: code=%d", code)
	}
	code, body = get(t, s, "/authorize?subject=alice&instance=doc-1&action=read")
	if code != http.StatusOK || body["allowed"] != true {
		t.Fatalf("alice after grant: code=%d body=%v", code, body)
	}
	// Unknown subject maps to a classified 403.
	code, _ = get(t, s, "/authorize?subject=ghost&instance=doc-1&action=read")
	if code != http.StatusForbidden {
		t.Fatalf("ghost: code=%d", code)
	}
	// Audit trail recorded the calls.
	code, body = get(t, s, "/audit")
	if code != http.StatusOK {
		t.Fatalf("audit: code=%d", code)
	}
}
