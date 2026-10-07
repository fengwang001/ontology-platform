package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ontology/ontology"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	guard := ontology.NewGuard(ontology.Config{LeaseTTL: time.Second})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /scopes", registerScope(guard))
	mux.HandleFunc("POST /links/begin", begin(guard))
	mux.HandleFunc("POST /links/heartbeat", heartbeat(guard))
	mux.HandleFunc("POST /links/commit", commit(guard))
	mux.HandleFunc("POST /links/rollback", rollback(guard))
	mux.HandleFunc("GET /scopes", getScope(guard))
	mux.HandleFunc("GET /journal", journal(guard))
	return mux
}

func post(t *testing.T, h http.Handler, path string, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v body=%s", path, err, rec.Body.String())
		}
	}
	return rec.Code, out
}

func TestHTTPSmoke(t *testing.T) {
	h := newTestServer(t)

	code, _ := post(t, h, "/scopes",
		`{"link_type":"L","field":"f","object_id":"o","capacity":1}`)
	if code != http.StatusOK {
		t.Fatalf("register status=%d", code)
	}

	code, r1 := post(t, h, "/links/begin",
		`{"request_id":"r1","link_type":"L","field":"f","object_id":"o","source_id":"a","target_id":"b","observed_version":0}`)
	if code != http.StatusOK || r1["Admitted"] != true {
		t.Fatalf("r1 begin code=%d body=%v", code, r1)
	}

	code, r2 := post(t, h, "/links/begin",
		`{"request_id":"r2","link_type":"L","field":"f","object_id":"o","source_id":"c","target_id":"d","observed_version":0}`)
	if code != http.StatusConflict {
		t.Fatalf("r2 want 409, got %d", code)
	}
	// RejectInFlight 的数值为 3（见 ontology/types.go）。
	if r2["Reason"] != float64(ontology.RejectInFlight) {
		t.Fatalf("r2 reason=%v", r2["Reason"])
	}

	if code, _ := post(t, h, "/links/rollback", `{"request_id":"r1"}`); code != http.StatusOK {
		t.Fatal("rollback r1")
	}
	code, r3 := post(t, h, "/links/begin",
		`{"request_id":"r3","link_type":"L","field":"f","object_id":"o","source_id":"c","target_id":"d","observed_version":0}`)
	if code != http.StatusOK || r3["Admitted"] != true {
		t.Fatalf("r3 after release code=%d body=%v", code, r3)
	}
	if code, _ := post(t, h, "/links/commit", `{"request_id":"r3"}`); code != http.StatusOK {
		t.Fatal("commit r3")
	}

	// 日志可导出，且包含 ADMIT/REJECT/ROLLBACK/COMMIT 四类事件。
	req := httptest.NewRequest(http.MethodGet, "/journal", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{"ADMIT", "REJECT", "ROLLBACK", "COMMIT"} {
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"`+want+`"`)) {
			t.Fatalf("journal missing %s: %s", want, body)
		}
	}
}
