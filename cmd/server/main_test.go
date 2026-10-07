package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDemoServerEndToEnd(t *testing.T) {
	mux := newMux(demoServer())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/actions/transfer/execute",
		strings.NewReader(`{"amount": 10}`))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("execute status=%d body=%s", rec.Code, rec.Body)
	}
	var resp executeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Committed || resp.Outputs["balance"] != float64(90) {
		t.Fatalf("unexpected execute response: %+v", resp)
	}
	if len(resp.Events) != 3 { // transfer + audit + notify
		t.Fatalf("expected 3 trace events, got %d", len(resp.Events))
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/objects/acc", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"balance":90`) {
		t.Fatalf("unexpected object response: %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commits", nil))
	if !strings.Contains(rec.Body.String(), `"Action":"transfer"`) {
		t.Fatalf("commit log must record the transfer chain: %s", rec.Body)
	}
}
