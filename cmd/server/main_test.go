package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ontology-platform/ontology"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	store := ontology.NewStore()
	if err := store.RegisterLinkType(ontology.LinkType{
		ID: "owns", CardinalityA: &ontology.Cardinality{Max: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateInstance("x"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"y0", "y1", "y2"} {
		if err := store.CreateInstance(id); err != nil {
			t.Fatal(err)
		}
	}
	engine := ontology.NewEngine(store, 3, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/update", func(w http.ResponseWriter, r *http.Request) {
		var dto ontology.UpdateRequestDTO
		if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req, err := ontology.ParseUpdate(dto)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		res, err := engine.Update(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		body, _ := ontology.MarshalResponse(res)
		switch {
		case res.Committed:
			w.WriteHeader(http.StatusOK)
		case res.Reject.Code == ontology.CodeCardinality:
			w.WriteHeader(http.StatusUnprocessableEntity)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_, _ = w.Write(body)
	})
	return mux
}

func post(t *testing.T, h http.Handler, body string) (int, ontology.UpdateResponseDTO) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/update", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var dto ontology.UpdateResponseDTO
	if rec.Code != http.StatusNotFound && strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("not found: %s", rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &dto)
	return rec.Code, dto
}

func TestHTTPEndToEnd(t *testing.T) {
	h := newTestHandler(t)

	// 第一次：成功占用唯一名额。
	code, body := post(t, h, `{"instance":"x","baseline":0,"ops":[{"typeId":"owns","side":"A","other":"y1","add":true}]}`)
	if code != http.StatusOK || !body.Committed {
		t.Fatalf("want 200 committed, got %d %+v", code, body)
	}

	// 第二次：基线追平但名额已满 -> 422 基数拒绝。
	code, body = post(t, h, `{"instance":"x","baseline":1,"ops":[{"typeId":"owns","side":"A","other":"y2","add":true}]}`)
	if code != http.StatusUnprocessableEntity || body.Reason != ontology.CodeCardinality {
		t.Fatalf("want 422 cardinality, got %d reason=%s", code, body.Reason)
	}

	// 幂等重放同一链接：成功但不推进版本。
	code, body = post(t, h, `{"instance":"x","baseline":1,"ops":[{"typeId":"owns","side":"A","other":"y1","add":true}]}`)
	if code != http.StatusOK || !body.Committed || body.Version != 1 {
		t.Fatalf("want idempotent ok at v1, got %d %+v", code, body)
	}

	// 每次响应都带完整尝试记录。
	if len(body.Attempts) == 0 || body.Attempts[0].Verdicts == nil {
		t.Fatalf("attempt audit records missing: %+v", body.Attempts)
	}
}
