package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ontology/ontology"
)

func doReq(t *testing.T, h http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, rdr)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestHTTPNotReadyAndFlow(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "svc.log")
	svc := ontology.New(logPath)
	srv := &server{svc: svc}

	// 重放完成前：503 + service_not_ready。
	rec := doReq(t, srv.handleOp, "POST", "/ops",
		`{"caller":"admin","op":{"kind":"declare_object_type","object_type":"N"}}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
	var er errorResponse
	json.NewDecoder(rec.Body).Decode(&er)
	if er.Reason != "service_not_ready" {
		t.Fatalf("reason=%q, want service_not_ready", er.Reason)
	}

	if err := svc.Replay(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	mustPost := func(body string) opResponse {
		t.Helper()
		rec := doReq(t, srv.handleOp, "POST", "/ops", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s: %d %s", body, rec.Code, rec.Body.String())
		}
		var resp opResponse
		json.NewDecoder(rec.Body).Decode(&resp)
		return resp
	}
	mustPost(`{"caller":"admin","op":{"kind":"declare_object_type","object_type":"N"}}`)
	mustPost(`{"caller":"admin","op":{"kind":"declare_link_type","link_spec":{"id":"e","from_type":"N","to_type":"N","cost":1,"directed":true}}}`)
	mustPost(`{"caller":"admin","op":{"kind":"create_object","object_type":"N","object":"A"}}`)
	mustPost(`{"caller":"admin","op":{"kind":"create_object","object_type":"N","object":"B"}}`)
	mustPost(`{"caller":"admin","op":{"kind":"create_link","link_type":"e","from":"A","to":"B"}}`)

	rec = doReq(t, srv.handleShortestPath, "GET", "/shortestpath?start=A&end=B&caller=admin", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("query: %d", rec.Code)
	}
	var p ontology.Path
	json.NewDecoder(rec.Body).Decode(&p)
	if !p.Found || p.Cost != 1 || len(p.Objects) != 2 {
		t.Fatalf("unexpected path %+v", p)
	}

	// 权限拒绝映射为 403，且 reason 属于四级分类。
	rec = doReq(t, srv.handleOp, "POST", "/ops",
		`{"caller":"nobody","op":{"kind":"create_object","object_type":"N","object":"C"}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", rec.Code)
	}
	json.NewDecoder(rec.Body).Decode(&er)
	if er.Reason != "permission_denied" {
		t.Fatalf("reason=%q, want permission_denied", er.Reason)
	}
}
