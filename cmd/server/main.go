// Command server 以 HTTP 暴露本体实例的链接更新（乐观提交 + 有限重试）。
//
// 仅用于演示与手工验证；判定逻辑全部在 ontology 包内。
package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"

	"ontology/ontology"
)

type opDTO struct {
	LinkType  string `json:"linkType"`
	Direction string `json:"direction"` // "out" | "in"
	OtherID   string `json:"otherId"`
	Add       bool   `json:"add"`
}

type submitRequest struct {
	ObjectID    string  `json:"objectId"`
	BaseVersion int64   `json:"baseVersion"`
	Ops         []opDTO `json:"ops"`
	MaxAttempts int     `json:"maxAttempts"`
}

func main() {
	store := ontology.NewStore()
	store.AddObject("obj-1")
	store.AddCardinality("obj-1", ontology.Cardinality{LinkType: "ownedBy", Direction: ontology.Outgoing, Max: 1})

	handler := newHandler(store)

	addr := ":8080"
	if p := os.Getenv("ADDR"); p != "" {
		addr = p
	}
	log.Printf("ontology server listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, handler))
}

func newHandler(store *ontology.Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/objects/", func(w http.ResponseWriter, r *http.Request) {
		// GET /objects/{id} → 当前快照
		id := r.URL.Path[len("/objects/"):]
		if id == "" {
			http.Error(w, "object id required", http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, store.Snapshot(id))
	})
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		var req submitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		change := ontology.Change{ObjectID: req.ObjectID, BaseVersion: req.BaseVersion}
		for _, op := range req.Ops {
			dir := ontology.Outgoing
			if op.Direction == "in" {
				dir = ontology.Incoming
			}
			change.Ops = append(change.Ops, ontology.LinkOp{
				LinkType: op.LinkType, Direction: dir, OtherID: op.OtherID, Add: op.Add,
			})
		}
		policy := ontology.DefaultRetryPolicy()
		if req.MaxAttempts > 0 {
			policy.MaxAttempts = req.MaxAttempts
		}
		result, err := ontology.NewSubmitter(store, policy).Submit(change)

		status := http.StatusOK
		switch {
		case errors.Is(err, ontology.ErrVersionConflict):
			status = http.StatusConflict
		case errors.Is(err, ontology.ErrCardinality):
			status = http.StatusUnprocessableEntity
		case errors.Is(err, ontology.ErrRetriesExhausted):
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]any{"result": result, "error": errString(err)})
	})

	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
