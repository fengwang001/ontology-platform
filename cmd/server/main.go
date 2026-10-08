// server 是本体链接图最短路径服务的 HTTP 入口。
// 进程启动后先在后台重放持久化日志，重放完成前所有请求返回 503 service_not_ready。
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"

	"ontology/ontology"
)

type server struct {
	svc *ontology.Service
}

type opRequest struct {
	Caller ontology.UserID    `json:"caller"`
	Op     ontology.Operation `json:"op"`
}

type opResponse struct {
	Seq uint64 `json:"seq"`
}

type errorResponse struct {
	Error  string `json:"error"`
	Reason string `json:"reason"`
}

func writeError(w http.ResponseWriter, status int, reason, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorResponse{Error: msg, Reason: reason})
}

func (s *server) handleOp(w http.ResponseWriter, r *http.Request) {
	var req opRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_params", err.Error())
		return
	}
	seq, err := s.svc.Apply(req.Op, req.Caller)
	if err != nil {
		switch {
		case ontology.IsNotReady(err):
			writeError(w, http.StatusServiceUnavailable, "service_not_ready", err.Error())
		default:
			var rej *ontology.RejectError
			if errors.As(err, &rej) {
				status := http.StatusBadRequest
				if rej.Kind == ontology.RejectPermission {
					status = http.StatusForbidden
				} else if rej.Kind == ontology.RejectCardinality {
					status = http.StatusConflict
				}
				writeError(w, status, string(rej.Kind), rej.Detail)
			} else {
				writeError(w, http.StatusInternalServerError, "internal", err.Error())
			}
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(opResponse{Seq: seq})
}

func (s *server) handleShortestPath(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	start := ontology.ObjectID(q.Get("start"))
	end := ontology.ObjectID(q.Get("end"))
	caller := ontology.UserID(q.Get("caller"))
	// 注意：内部 Metrics 不在此暴露给调用者。
	path, _, err := s.svc.ShortestPath(start, end, caller)
	if err != nil {
		if ontology.IsNotReady(err) {
			writeError(w, http.StatusServiceUnavailable, "service_not_ready", err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(path)
}

func main() {
	logPath := flag.String("log", "ontology.log", "path to the operation log file")
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	svc := ontology.New(*logPath)
	// 后台重放；重放完成前到达的请求一律被拒绝为 service_not_ready。
	go func() {
		if err := svc.Replay(); err != nil {
			log.Fatalf("replay failed: %v", err)
		}
		log.Printf("replay finished, service ready")
	}()

	srv := &server{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ops", srv.handleOp)
	mux.HandleFunc("GET /shortestpath", srv.handleShortestPath)
	log.Printf("listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
