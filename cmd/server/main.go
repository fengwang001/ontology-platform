package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"ontology/ontology"
)

// 简易 JSON HTTP 入口：
//   POST   /v1/objects        {"id","type","properties"}
//   DELETE /v1/objects/{id}
//   POST   /v1/links          {"id","type","from","to"}
//   DELETE /v1/links/{id}
//   POST   /v1/traverse/page  {"cursor","spec","batchSize","mode"}

type server struct {
	store *ontology.Store
	svc   *ontology.Service
}

func main() {
	store := ontology.NewStore()
	s := &server{store: store, svc: ontology.NewService(store, nil)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/objects", s.addObject)
	mux.HandleFunc("DELETE /v1/objects/{id}", s.deleteObject)
	mux.HandleFunc("POST /v1/links", s.addLink)
	mux.HandleFunc("DELETE /v1/links/{id}", s.deleteLink)
	mux.HandleFunc("POST /v1/traverse/page", s.page)
	log.Println("ontology server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	body := map[string]string{"error": err.Error()}
	var e *ontology.Error
	if errors.As(err, &e) {
		body["kind"] = map[ontology.ErrorKind]string{
			ontology.ErrStartObjectNotFound: "START_OBJECT_NOT_FOUND",
			ontology.ErrInvalidCursor:       "INVALID_CURSOR",
			ontology.ErrInvalidBatchSize:    "INVALID_BATCH_SIZE",
			ontology.ErrModeChange:          "MODE_CHANGE",
		}[e.Kind]
	}
	writeJSON(w, http.StatusBadRequest, body)
}

func (s *server) addObject(w http.ResponseWriter, r *http.Request) {
	var o ontology.Object
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.AddObject(o); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

func (s *server) deleteObject(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteObject(ontology.ObjectID(r.PathValue("id"))); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) addLink(w http.ResponseWriter, r *http.Request) {
	var l ontology.Link
	if err := json.NewDecoder(r.Body).Decode(&l); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.AddLink(l); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

func (s *server) deleteLink(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteLink(ontology.LinkID(r.PathValue("id"))); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type hopJSON struct {
	LinkType string `json:"linkType"`
	Dir      string `json:"dir"` // "out" | "in" | "both"
	Limit    int    `json:"limit"`
}

type specJSON struct {
	Start string    `json:"start"`
	Hops  []hopJSON `json:"hops"`
}

type pageRequestJSON struct {
	Cursor    string   `json:"cursor"`
	Spec      specJSON `json:"spec"`
	BatchSize int      `json:"batchSize"`
	Mode      string   `json:"mode"` // "silent" | "marked"
}

type markerJSON struct {
	Hop     int    `json:"hop"`
	At      string `json:"at"`
	Dropped int    `json:"dropped"`
}

type pageResponseJSON struct {
	Objects []string     `json:"objects"`
	Markers []markerJSON `json:"markers"`
	Cursor  string       `json:"cursor"`
	Done    bool         `json:"done"`
}

func parseDir(s string) (ontology.Direction, bool) {
	switch strings.ToLower(s) {
	case "out", "":
		return ontology.DirOut, true
	case "in":
		return ontology.DirIn, true
	case "both":
		return ontology.DirBoth, true
	}
	return 0, false
}

func (s *server) page(w http.ResponseWriter, r *http.Request) {
	var req pageRequestJSON
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	var mode ontology.Mode
	switch strings.ToLower(req.Mode) {
	case "silent", "":
		mode = ontology.ModeSilent
	case "marked":
		mode = ontology.ModeMarked
	default:
		writeErr(w, errors.New("unknown mode: "+req.Mode))
		return
	}
	spec := ontology.TraversalSpec{Start: ontology.ObjectID(req.Spec.Start)}
	for _, h := range req.Spec.Hops {
		dir, ok := parseDir(h.Dir)
		if !ok {
			writeErr(w, errors.New("unknown direction: "+h.Dir))
			return
		}
		spec.Hops = append(spec.Hops, ontology.HopSpec{LinkType: h.LinkType, Dir: dir, Limit: h.Limit})
	}
	resp, err := s.svc.Page(ontology.PageRequest{
		Cursor:    req.Cursor,
		Spec:      spec,
		BatchSize: req.BatchSize,
		Mode:      mode,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	out := pageResponseJSON{Cursor: resp.Cursor, Done: resp.Done, Objects: []string{}}
	for _, id := range resp.Objects {
		out.Objects = append(out.Objects, string(id))
	}
	for _, m := range resp.Markers {
		out.Markers = append(out.Markers, markerJSON{Hop: m.Hop, At: string(m.At), Dropped: m.Dropped})
	}
	writeJSON(w, http.StatusOK, out)
}
