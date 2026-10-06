package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ontology/srcmap"
)

func do(t *testing.T, h http.Handler, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("响应非 JSON: %s", rec.Body.String())
		}
	}
	return rec.Code, out
}

func TestHTTPEndToEnd(t *testing.T) {
	h := NewHandler(srcmap.NewService())

	registerM1 := `{"name":"m1","sourceCount":1,"lines":[{"GeneratedLine":0,"Segments":[` +
		`{"Start":0,"SourceIndex":0,"OrigLine":1,"OrigCol":0},` +
		`{"Start":3,"SourceIndex":0,"OrigLine":2,"OrigCol":0}]}]}`
	if code, out := do(t, h, "/register", registerM1); code != 200 || out["ok"] != true {
		t.Fatalf("登记 m1: %d %v", code, out)
	}
	if code, out := do(t, h, "/register", registerM1); code != 409 || out["errorCategory"] != string(srcmap.CategoryDuplicate) {
		t.Fatalf("重名应 409: %d %v", code, out)
	}
	registerM2 := `{"name":"m2","sourceCount":1,"lines":[{"GeneratedLine":0,"Segments":` +
		`[{"Start":0,"SourceIndex":0,"OrigLine":0,"OrigCol":0}]}]}`
	if code, out := do(t, h, "/register", registerM2); code != 200 {
		t.Fatalf("登记 m2: %d %v", code, out)
	}
	if code, out := do(t, h, "/compose", `{"m2":"m2","m1":"m1","result":"c"}`); code != 200 {
		t.Fatalf("合成: %d %v", code, out)
	}
	// 最终列 5 → 中间列 5 → M1 第二段（中间列3起，原始行2，原始列0..）→ (行2,列2)
	if code, out := do(t, h, "/query", `{"name":"c","line":0,"column":5}`); code != 200 ||
		out["mapped"] != true || out["line"].(float64) != 2 || out["column"].(float64) != 2 {
		t.Fatalf("查询列5: %d %v", code, out)
	}
	if code, out := do(t, h, "/query", `{"name":"c","line":0,"column":2}`); code != 200 ||
		out["mapped"] != true || out["line"].(float64) != 1 || out["column"].(float64) != 2 {
		t.Fatalf("查询列2: %d %v", code, out)
	}
	// 越界参数。
	if code, out := do(t, h, "/query", `{"name":"c","line":0,"column":1000000001}`); code != 400 ||
		out["errorCategory"] != string(srcmap.CategoryInvalidArgument) {
		t.Fatalf("越界应 400: %d %v", code, out)
	}
	// 未找到。
	if code, out := do(t, h, "/compose", `{"m2":"nope","m1":"m1","result":"x"}`); code != 404 ||
		out["errorCategory"] != string(srcmap.CategoryNotFound) {
		t.Fatalf("缺名应 404: %d %v", code, out)
	}
	// 未知字段拒绝。
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(`{"name":"c","line":0,"column":1,"x":1}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("未知字段应 400: %d", rec.Code)
	}
	t.Logf("[判定] HTTP 登记/重名/合成/查询/越界/缺名/未知字段 全部正确")
}
