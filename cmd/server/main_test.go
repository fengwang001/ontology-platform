package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ont "ontology/ontology"
)

func newTestGateway() *ont.Gateway { return ont.NewGateway() }

func post(t *testing.T, h http.Handler, path string, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func get(t *testing.T, h http.Handler, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestHTTPEndToEnd(t *testing.T) {
	s := &server{g: newTestGateway()}
	h := s.routes()

	code, out := post(t, h, "/types", `{
		"type_id":"Person","write_policy":"ignore_field",
		"attrs":[{"id":"a_email","name":"email","kind":"string"},
		         {"id":"a_ssn","name":"ssn","kind":"string"}]}`)
	if code != http.StatusCreated || out["version"].(float64) != 1 {
		t.Fatalf("create type: %d %v", code, out)
	}
	for _, b := range []string{
		`{"type_id":"Person","subject":"alice","attr_id":"a_email","op":"write","from_ver":1,"to_ver":0}`,
		`{"type_id":"Person","subject":"alice","attr_id":"a_email","op":"read","from_ver":1,"to_ver":0}`,
		// admin 类型级写，用于把无读权限字段 ssn 写进对象。
		`{"type_id":"Person","subject":"admin","attr_id":"*","op":"write","from_ver":1,"to_ver":0}`,
	} {
		if code, out := post(t, h, "/permissions/grant", b); code != http.StatusOK {
			t.Fatalf("grant: %d %v", code, out)
		}
	}

	code, out = post(t, h, "/objects",
		`{"object_id":"p1","type_id":"Person","subject":"admin","schema_ver":1,
		  "fields":{"email":"a@x.com","ssn":"secret"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create object: %d %v", code, out)
	}

	// 演进：重命名 a_email。
	code, out = post(t, h, "/types/Person/evolve",
		`{"type_id":"Person","changes":[{"kind":"rename","attr_id":"a_email","new_name":"contact"}]}`)
	if code != http.StatusOK || out["version"].(float64) != 2 {
		t.Fatalf("evolve: %d %v", code, out)
	}

	// 版本过期 => 409 version_expired。
	code, out = post(t, h, "/objects/p1/write",
		`{"type_id":"Person","subject":"alice","schema_ver":1,"fields":{"contact":"b@x.com"}}`)
	if code != http.StatusConflict || out["error_kind"] != "version_expired" {
		t.Fatalf("stale write: %d %v", code, out)
	}

	// 旧名字 => 404 attr_not_found。
	code, out = post(t, h, "/objects/p1/write",
		`{"type_id":"Person","subject":"alice","schema_ver":2,"fields":{"email":"b@x.com"}}`)
	if code != http.StatusNotFound || out["error_kind"] != "attr_not_found" {
		t.Fatalf("old-name write: %d %v", code, out)
	}

	// 读投影：ssn 无读权限 => 整体移除，partial_view=true。
	code, out = get(t, h, "/objects/p1?type_id=Person&subject=alice")
	if code != http.StatusOK {
		t.Fatalf("read: %d %v", code, out)
	}
	res := out["result"].(map[string]any)
	if res["partial_view"] != true {
		t.Fatalf("expected partial view: %v", res)
	}

	// 审计可读到历史授予记录。
	code, out = get(t, h, "/permissions/audit?type_id=Person&subject=alice")
	if code != http.StatusOK || out["count"].(float64) != 2 {
		t.Fatalf("audit: %d %v", code, out)
	}

	// 策略冲突 => 400 write_policy_conflict。
	code, out = post(t, h, "/types/Person/evolve",
		`{"type_id":"Person","write_policy":"reject_whole","changes":[]}`)
	if code != http.StatusBadRequest || out["error_kind"] != "write_policy_conflict" {
		t.Fatalf("policy conflict: %d %v", code, out)
	}

	// 决策日志含输入/输出/依据。
	code, out = get(t, h, "/decisions")
	if code != http.StatusOK {
		t.Fatalf("decisions: %d", code)
	}
	blob, _ := json.Marshal(out)
	if !strings.Contains(string(blob), "version expired precedes") {
		t.Fatal("decision log missing reason/evidence")
	}
}
