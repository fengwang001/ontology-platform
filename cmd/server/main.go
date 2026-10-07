// 命令 server 是基数约束并发名额控制的最小可运行演示：
// 通过 HTTP 暴露作用域注册、两阶段新建关联（准入/心跳/提交/回滚）、
// 状态查询与决策日志导出，便于手工验证幻影防护与失败释放语义。
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"time"

	"ontology/ontology"
)

func main() {
	addr := flag.String("addr", ":8080", "监听地址")
	ttl := flag.Duration("lease-ttl", 30*time.Second, "进行中请求的心跳租约时长")
	flag.Parse()

	guard := ontology.NewGuard(ontology.Config{LeaseTTL: *ttl})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /scopes", registerScope(guard))
	mux.HandleFunc("POST /links/begin", begin(guard))
	mux.HandleFunc("POST /links/heartbeat", heartbeat(guard))
	mux.HandleFunc("POST /links/commit", commit(guard))
	mux.HandleFunc("POST /links/rollback", rollback(guard))
	mux.HandleFunc("GET /scopes", getScope(guard))
	mux.HandleFunc("GET /journal", journal(guard))

	log.Printf("ontology cardinality guard listening on %s (lease-ttl=%s)", *addr, *ttl)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

type scopeReq struct {
	LinkType string `json:"link_type"`
	Field    string `json:"field"`
	ObjectID string `json:"object_id"`
	Capacity int    `json:"capacity"`
}

type linkReq struct {
	RequestID       string `json:"request_id"`
	LinkType        string `json:"link_type"`
	Field           string `json:"field"`
	ObjectID        string `json:"object_id"`
	SourceID        string `json:"source_id"`
	TargetID        string `json:"target_id"`
	ObservedVersion int64  `json:"observed_version"`
}

type idReq struct {
	RequestID string `json:"request_id"`
}

func key(r scopeReq) ontology.ScopeKey {
	return ontology.ScopeKey{LinkType: r.LinkType, Field: r.Field, ObjectID: r.ObjectID}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return false
	}
	return true
}

func registerScope(g *ontology.Guard) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req scopeReq
		if !decode(w, r, &req) {
			return
		}
		if err := g.EnsureScope(key(req), req.Capacity); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"registered": true, "capacity": req.Capacity})
	}
}

func begin(g *ontology.Guard) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req linkReq
		if !decode(w, r, &req) {
			return
		}
		d := g.Begin(ontology.BeginRequest{
			RequestID:       req.RequestID,
			Scope:           key(scopeReq{LinkType: req.LinkType, Field: req.Field, ObjectID: req.ObjectID}),
			SourceID:        req.SourceID,
			TargetID:        req.TargetID,
			ObservedVersion: req.ObservedVersion,
		})
		status := http.StatusOK
		if !d.Admitted {
			status = http.StatusConflict
		}
		writeJSON(w, status, d)
	}
}

func heartbeat(g *ontology.Guard) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req idReq
		if !decode(w, r, &req) {
			return
		}
		if !g.Heartbeat(req.RequestID) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "request not in progress"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"heartbeat": true})
	}
}

func commit(g *ontology.Guard) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req idReq
		if !decode(w, r, &req) {
			return
		}
		cr := g.Commit(req.RequestID)
		status := http.StatusOK
		if !cr.OK {
			status = http.StatusConflict
		}
		writeJSON(w, status, cr)
	}
}

func rollback(g *ontology.Guard) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req idReq
		if !decode(w, r, &req) {
			return
		}
		if !g.Rollback(req.RequestID) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "request not in progress"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"released": true})
	}
}

func getScope(g *ontology.Guard) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		k := ontology.ScopeKey{
			LinkType: r.URL.Query().Get("link_type"),
			Field:    r.URL.Query().Get("field"),
			ObjectID: r.URL.Query().Get("object_id"),
		}
		snap, err := g.Snapshot(k)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, snap)
	}
}

func journal(g *ontology.Guard) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"events": g.Journal().Events()})
	}
}
