package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ontology/ontology"
)

// 通过 httptest.ResponseRecorder 走完整 HTTP 处理链路（无需监听端口）。
func TestHTTPSmoke(t *testing.T) {
	s := &server{store: ontology.NewStore()}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/object-types", s.defineObjectType)
	mux.HandleFunc("POST /v1/object-types/{id}/migrations", s.migrateObjectType)
	mux.HandleFunc("PUT /v1/objects/{type}/{id}", s.putObject)
	mux.HandleFunc("POST /v1/traverse", s.traverse)
	mux.HandleFunc("GET /v1/version", s.version)
	mux.HandleFunc("GET /v1/audit", s.audit)

	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := call("POST", "/v1/object-types", `{"id":"doc","props":[{"ID":"p1","Name":"title","Type":"string"}]}`); rec.Code != 200 {
		t.Fatalf("defineObjectType: %d %s", rec.Code, rec.Body)
	}
	if rec := call("PUT", "/v1/objects/doc/a", `{"props":{"title":"hello"}}`); rec.Code != 200 {
		t.Fatalf("putObject: %d %s", rec.Code, rec.Body)
	}
	if rec := call("POST", "/v1/object-types/doc/migrations", `{"add":[{"ID":"p1","Name":"headline","Type":"string"}]}`); rec.Code != 200 {
		t.Fatalf("migrate: %d %s", rec.Code, rec.Body)
	}

	// 历史时刻 v2 的遍历必须仍按迁移前的定义解释。
	rec := call("POST", "/v1/traverse", `{"Start":"a","AsOf":2,"MaxDepth":4,"MaxVisited":10}`)
	if rec.Code != 200 {
		t.Fatalf("traverse: %d %s", rec.Code, rec.Body)
	}
	var res ontology.TraverseResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].Props["title"] != "hello" {
		t.Fatalf("unexpected snapshot: %+v", res.Nodes)
	}
	if len(res.Decisions) == 0 {
		t.Fatal("decisions must be recorded")
	}

	// 起始对象不存在：结构化错误。
	rec = call("POST", "/v1/traverse", `{"Start":"ghost","AsOf":2,"MaxDepth":4,"MaxVisited":10}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d", rec.Code)
	}
	var e struct {
		Error string `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Error != "START_OBJECT_NOT_EXIST" {
		t.Fatalf("got %q", e.Error)
	}

	// 审计端点应有判定记录。
	rec = call("GET", "/v1/audit", "")
	if !strings.Contains(rec.Body.String(), "schema-select") {
		t.Fatalf("audit missing decisions: %s", rec.Body)
	}
}
