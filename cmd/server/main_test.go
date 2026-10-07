package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ontology/ontology"
)

func TestSubmitStatusCodes(t *testing.T) {
	store := ontology.NewStore()
	store.AddObject("o")
	store.AddCardinality("o", ontology.Cardinality{LinkType: "ownedBy", Direction: ontology.Outgoing, Max: 1})

	handler := newHandler(store)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/submit", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// 第一次：成功。
	rec := post(`{"objectId":"o","baseVersion":0,"ops":[{"linkType":"ownedBy","direction":"out","otherId":"a","add":true}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("first submit code=%d body=%s", rec.Code, rec.Body.String())
	}

	// 基数不满足：422。
	rec = post(`{"objectId":"o","baseVersion":1,"ops":[{"linkType":"ownedBy","direction":"out","otherId":"b","add":true}]}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "cardinality") {
		t.Fatalf("want 422 cardinality, got %d %s", rec.Code, rec.Body.String())
	}

	// 基线落后且预算为 1：409 版本冲突。
	rec = post(`{"objectId":"o","baseVersion":0,"maxAttempts":1,"ops":[{"linkType":"ownedBy","direction":"out","otherId":"c","add":true}]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409 conflict, got %d %s", rec.Code, rec.Body.String())
	}

	// 快照可读。
	getReq := httptest.NewRequest(http.MethodGet, "/objects/o", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("snapshot code=%d", getRec.Code)
	}
	var snap map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap["Version"].(float64) != 1 {
		t.Fatalf("snapshot version=%v", snap["Version"])
	}
}
