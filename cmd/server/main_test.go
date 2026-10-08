package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ontology/internal/gateway"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	gw, err := gateway.NewGateway(gateway.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	s := &server{gw: gw}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /vehicles/{vid}/reports", s.postReport)
	mux.HandleFunc("POST /vehicles/{vid}/commands", s.postCommand)
	mux.HandleFunc("POST /vehicles/{vid}/commands/{id}/acks", s.postAck)
	mux.HandleFunc("GET /vehicles/{vid}/commands/{id}", s.getCommand)
	mux.HandleFunc("GET /vehicles/{vid}", s.getVehicle)
	return mux
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestHTTPSmoke HTTP 层端到端：上报→提交→唤醒→下发→回执→迟到回执→幂等重放。
func TestHTTPSmoke(t *testing.T) {
	h := newTestServer(t)

	rec := do(t, h, "POST", "/vehicles/v1/reports",
		`{"seq":1,"time":100,"gear":"park","speed_kmh":0,"power":"sleep","lock":"all_locked","battery_pct":80}`)
	if rec.Code != 200 {
		t.Fatalf("report: %d %s", rec.Code, rec.Body)
	}
	rec = do(t, h, "POST", "/vehicles/v1/commands",
		`{"submitter":"app","request_id":"r1","type":"find_car","time":110,"validity_sec":500}`)
	if rec.Code != 200 {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body)
	}
	var submitResp struct {
		CommandID string `json:"command_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &submitResp); err != nil {
		t.Fatal(err)
	}
	if submitResp.Status != "accepted" {
		t.Fatalf("休眠车辆应等待唤醒, got %s", submitResp.Status)
	}

	// 车端上线 → 下发。
	rec = do(t, h, "POST", "/vehicles/v1/reports",
		`{"seq":2,"time":120,"gear":"park","speed_kmh":0,"power":"awake","lock":"all_locked","battery_pct":80}`)
	if rec.Code != 200 {
		t.Fatalf("online report: %d %s", rec.Code, rec.Body)
	}
	rec = do(t, h, "GET", "/vehicles/v1/commands/"+submitResp.CommandID, "")
	var view gateway.CommandView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Status != gateway.StatusDispatched {
		t.Fatalf("唤醒完成后应下发, got %v", view.Status)
	}

	// 回执 + 迟到回执。
	rec = do(t, h, "POST", "/vehicles/v1/commands/"+submitResp.CommandID+"/acks", `{"time":130,"success":true}`)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "true") {
		t.Fatalf("首次回执不应为迟到: %d %s", rec.Code, rec.Body)
	}
	rec = do(t, h, "POST", "/vehicles/v1/commands/"+submitResp.CommandID+"/acks", `{"time":140,"success":true}`)
	if !strings.Contains(rec.Body.String(), `"late":true`) {
		t.Fatalf("应为迟到回执: %s", rec.Body)
	}

	// 幂等重放：同键同内容返回原指令。
	rec = do(t, h, "POST", "/vehicles/v1/commands",
		`{"submitter":"app","request_id":"r1","type":"find_car","time":150,"validity_sec":500}`)
	if !strings.Contains(rec.Body.String(), `"duplicate":true`) ||
		!strings.Contains(rec.Body.String(), submitResp.CommandID) {
		t.Fatalf("应返回原指令结果: %s", rec.Body)
	}

	// 拒绝：前置不满足（车辆已唤醒但电量不足时开空调）。
	rec = do(t, h, "POST", "/vehicles/v1/reports",
		`{"seq":3,"time":160,"gear":"park","speed_kmh":0,"power":"awake","lock":"all_locked","battery_pct":5}`)
	rec = do(t, h, "POST", "/vehicles/v1/commands",
		`{"submitter":"app","request_id":"r2","type":"ac_on","time":170,"validity_sec":60}`)
	if rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "precondition") {
		t.Fatalf("应以 422 返回前置不满足: %d %s", rec.Code, rec.Body)
	}

	// 车辆快照。
	rec = do(t, h, "GET", "/vehicles/v1", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"VehicleID":"v1"`) {
		t.Fatalf("快照异常: %d %s", rec.Code, rec.Body)
	}
}
