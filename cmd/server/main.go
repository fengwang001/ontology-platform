package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"ontology/ontology"
)

// server 是本体平台的 HTTP 门面，包装 ontology.Store。
type server struct {
	store *ontology.Store

	mu     sync.Mutex
	audits []ontology.Decision // 近期遍历判定日志（环形保留）
	auditW *os.File            // 可选的审计日志落盘文件
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	auditPath := flag.String("audit", "", "append traversal decision audit log (JSONL) to this file")
	flag.Parse()

	s := &server{store: ontology.NewStore()}
	if *auditPath != "" {
		f, err := os.OpenFile(*auditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			log.Fatalf("open audit log: %v", err)
		}
		s.auditW = f
		defer f.Close()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/object-types", s.defineObjectType)
	mux.HandleFunc("POST /v1/object-types/{id}/migrations", s.migrateObjectType)
	mux.HandleFunc("POST /v1/link-types", s.defineLinkType)
	mux.HandleFunc("POST /v1/link-types/{id}/cardinality", s.adjustCardinality)
	mux.HandleFunc("PUT /v1/objects/{type}/{id}", s.putObject)
	mux.HandleFunc("DELETE /v1/objects/{id}", s.deleteObject)
	mux.HandleFunc("POST /v1/objects/{id}/properties", s.setProperty)
	mux.HandleFunc("PUT /v1/links/{type}/{from}/{to}", s.addLink)
	mux.HandleFunc("DELETE /v1/links/{type}/{from}/{to}", s.removeLink)
	mux.HandleFunc("POST /v1/traverse", s.traverse)
	mux.HandleFunc("GET /v1/version", s.version)
	mux.HandleFunc("GET /v1/audit", s.audit)

	log.Printf("ontology server listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var te *ontology.TraverseError
	if errors.As(err, &te) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": te.Kind.String(), "detail": te.Detail,
		})
		return
	}
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func (s *server) defineObjectType(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    string                 `json:"id"`
		Props []ontology.PropertyDef `json:"props"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	v, err := s.store.DefineObjectType(req.ID, req.Props)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": v})
}

func (s *server) migrateObjectType(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Add  []ontology.PropertyDef `json:"add"`
		Drop []string               `json:"drop"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	v, err := s.store.MigrateObjectType(r.PathValue("id"), req.Add, req.Drop)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": v})
}

func (s *server) defineLinkType(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID          string `json:"id"`
		FromType    string `json:"fromType"`
		ToType      string `json:"toType"`
		Cardinality string `json:"cardinality"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	v, err := s.store.DefineLinkType(req.ID, req.FromType, req.ToType, ontology.Cardinality(req.Cardinality))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": v})
}

func (s *server) adjustCardinality(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cardinality string `json:"cardinality"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	v, err := s.store.AdjustCardinality(r.PathValue("id"), ontology.Cardinality(req.Cardinality))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": v})
}

// coerceNumbers 把 JSON 解码出的 float64 整数值转为 int64，
// 使 REST 客户端可以用 JSON number 表示 int 属性。
func coerceNumbers(props map[string]ontology.Value) {
	for k, v := range props {
		if f, ok := v.(float64); ok && f == float64(int64(f)) {
			props[k] = int64(f)
		}
	}
}

func (s *server) putObject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Props map[string]ontology.Value `json:"props"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	coerceNumbers(req.Props)
	v, err := s.store.PutObject(r.PathValue("type"), r.PathValue("id"), req.Props)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": v})
}

func (s *server) deleteObject(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.DeleteObject(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": v})
}

func (s *server) setProperty(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string         `json:"name"`
		Value ontology.Value `json:"value"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	props := map[string]ontology.Value{"v": req.Value}
	coerceNumbers(props)
	v, err := s.store.SetProperty(r.PathValue("id"), req.Name, props["v"])
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": v})
}

func (s *server) addLink(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.AddLink(r.PathValue("type"), r.PathValue("from"), r.PathValue("to"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": v})
}

func (s *server) removeLink(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.RemoveLink(r.PathValue("type"), r.PathValue("from"), r.PathValue("to"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": v})
}

func (s *server) traverse(w http.ResponseWriter, r *http.Request) {
	var req ontology.TraverseRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	res, err := s.store.Traverse(req)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.recordAudit(res.Decisions)
	writeJSON(w, http.StatusOK, res)
}

func (s *server) recordAudit(ds []ontology.Decision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits = append(s.audits, ds...)
	const keep = 10000
	if len(s.audits) > keep {
		s.audits = append([]ontology.Decision(nil), s.audits[len(s.audits)-keep:]...)
	}
	if s.auditW != nil {
		for _, d := range ds {
			line, _ := json.Marshal(d)
			fmt.Fprintf(s.auditW, "%s\n", line)
		}
	}
}

func (s *server) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.store.CurrentVersion(),
		"horizon": s.store.Horizon(),
	})
}

func (s *server) audit(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tail := s.audits
	if n := len(tail); n > 200 {
		tail = tail[n-200:]
	}
	var b strings.Builder
	b.WriteString("[")
	for i, d := range tail {
		if i > 0 {
			b.WriteString(",")
		}
		line, _ := json.Marshal(d)
		b.Write(line)
	}
	b.WriteString("]")
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, b.String())
}
