// Command server 把属性级权限随对象类型版本迁移联动的判定网关
// 以最小 HTTP/JSON 接口暴露出来，便于手工联调与本地验证。
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	ont "ontology/ontology"
)

type server struct{ g *ont.Gateway }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	kind := ont.ErrorKindOf(err)
	status := http.StatusInternalServerError
	switch kind {
	case ont.ErrObjectNotFound, ont.ErrTypeNotFound, ont.ErrAttrNotFound:
		status = http.StatusNotFound
	case ont.ErrVersionExpired:
		status = http.StatusConflict
	case ont.ErrTypeNoPermission, ont.ErrPermissionDenied, ont.ErrRevoked:
		status = http.StatusForbidden
	case ont.ErrWritePolicyConflict, ont.ErrInvalidArgument:
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]any{"ok": false, "error_kind": string(kind), "error": err.Error()})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return false
	}
	return true
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /types", s.createType)
	mux.HandleFunc("POST /types/{typeID}/evolve", s.evolve)
	mux.HandleFunc("POST /permissions/grant", s.grant)
	mux.HandleFunc("POST /permissions/revoke", s.revoke)
	mux.HandleFunc("GET /permissions/audit", s.audit)
	mux.HandleFunc("POST /objects", s.createObject)
	mux.HandleFunc("POST /objects/{id}/write", s.write)
	mux.HandleFunc("GET /objects/{id}", s.read)
	mux.HandleFunc("GET /decisions", s.decisions)
	return mux
}

type createTypeReq struct {
	TypeID      string          `json:"type_id"`
	Attrs       []ont.Attribute `json:"attrs"`
	WritePolicy ont.WritePolicy `json:"write_policy"`
}

func (s *server) createType(w http.ResponseWriter, r *http.Request) {
	var req createTypeReq
	if !decode(w, r, &req) {
		return
	}
	v, err := s.g.CreateType(req.TypeID, req.Attrs, req.WritePolicy)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "version": v})
}

type evolveReq struct {
	TypeID      string           `json:"type_id"`
	Changes     []ont.AttrChange `json:"changes"`
	WritePolicy ont.WritePolicy  `json:"write_policy"`
}

func (s *server) evolve(w http.ResponseWriter, r *http.Request) {
	var req evolveReq
	if !decode(w, r, &req) {
		return
	}
	v, err := s.g.Evolve(req.TypeID, req.Changes, req.WritePolicy)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": v})
}

type permReq struct {
	TypeID  string `json:"type_id"`
	Subject string `json:"subject"`
	AttrID  string `json:"attr_id"`
	Op      ont.Op `json:"op"`
	FromVer int    `json:"from_ver"`
	ToVer   int    `json:"to_ver"`
}

func (s *server) grant(w http.ResponseWriter, r *http.Request) {
	var req permReq
	if !decode(w, r, &req) {
		return
	}
	if err := s.g.Grant(req.TypeID, req.Subject, req.AttrID, req.Op, req.FromVer, req.ToVer); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) revoke(w http.ResponseWriter, r *http.Request) {
	var req permReq
	if !decode(w, r, &req) {
		return
	}
	if err := s.g.Revoke(req.TypeID, req.Subject, req.AttrID, req.Op, req.FromVer, req.ToVer); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) audit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows := s.g.AuditPermissions(q.Get("type_id"), q.Get("subject"), q.Get("attr_id"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "entries": rows, "count": len(rows)})
}

func (s *server) createObject(w http.ResponseWriter, r *http.Request) {
	var req ont.WriteRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := s.g.CreateObject(req)
	if err != nil {
		writeErr(w, err)
		return
	}
	status := http.StatusCreated
	if !res.Applied {
		status = http.StatusForbidden
	}
	writeJSON(w, status, map[string]any{"ok": true, "result": res})
}

type writeURL struct {
	ID string `json:"id"`
}

func (s *server) write(w http.ResponseWriter, r *http.Request) {
	var req ont.WriteRequest
	if !decode(w, r, &req) {
		return
	}
	req.ObjectID = r.PathValue("id")
	res, err := s.g.Write(req)
	if err != nil {
		writeErr(w, err)
		return
	}
	status := http.StatusOK
	if !res.Applied {
		status = http.StatusForbidden
	}
	writeJSON(w, status, map[string]any{"ok": true, "result": res})
}

func (s *server) read(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := s.g.Read(r.PathValue("id"), q.Get("type_id"), q.Get("subject"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": res})
}

func (s *server) decisions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"entries": s.g.DecisionLog()})
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	s := &server{g: ont.NewGateway()}
	log.Printf("ontology permission gateway listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, s.routes()))
}
