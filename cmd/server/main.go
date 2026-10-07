// Command server exposes the ontology access-control engine over a small
// JSON/HTTP API and seeds a demo scenario on startup.
package main

import (
	"log"
	"net/http"
	"os"

	"ontology/ontology"
)

type server struct {
	engine *ontology.Engine
	audit  *ontology.MemoryLogger
}

func (s *server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /declare/object-type", mutate(func(a idReq) error {
		return s.engine.DeclareObjectType(a.ID)
	}))
	mux.HandleFunc("POST /declare/link-type", mutate(func(a linkTypeReq) error {
		return s.engine.DeclareLinkType(a.ID, a.From, a.To)
	}))
	mux.HandleFunc("POST /declare/tag", mutate(func(a idReq) error {
		return s.engine.DeclareTag(a.ID)
	}))
	mux.HandleFunc("POST /declare/role", mutate(func(a roleReq) error {
		return s.engine.DeclareRole(a.ID, a.Parents...)
	}))
	mux.HandleFunc("POST /declare/subject", mutate(func(a subjectReq) error {
		return s.engine.DeclareSubject(a.ID, a.Roles...)
	}))
	mux.HandleFunc("POST /declare/instance", mutate(func(a instanceReq) error {
		return s.engine.DeclareInstance(a.ID, a.ObjectType)
	}))

	mux.HandleFunc("POST /tag/attach", mutate(func(a attachReq) error {
		return s.engine.AttachTag(a.Tag, a.ObjectType)
	}))
	mux.HandleFunc("POST /tag/detach", mutate(func(a attachReq) error {
		return s.engine.DetachTag(a.Tag, a.ObjectType)
	}))

	mux.HandleFunc("POST /propagation/declare", mutate(func(a propagationReq) error {
		dir, err := parseDirection(a.Direction)
		if err != nil {
			return err
		}
		return s.engine.DeclarePropagation(a.Tag, a.LinkType, dir)
	}))
	mux.HandleFunc("POST /propagation/revoke", mutate(func(a pairReq) error {
		return s.engine.RevokePropagation(a.Tag, a.LinkType)
	}))

	mux.HandleFunc("POST /block/declare", mutate(func(a pairReq) error {
		return s.engine.DeclareBlock(a.Tag, a.LinkType)
	}))
	mux.HandleFunc("POST /block/remove", mutate(func(a pairReq) error {
		return s.engine.RemoveBlock(a.Tag, a.LinkType)
	}))

	mux.HandleFunc("POST /grant", mutate(func(a grantReq) error {
		effect, err := parseEffect(a.Effect)
		if err != nil {
			return err
		}
		return s.engine.SetGrant(a.Role, a.Tag, effect)
	}))
	mux.HandleFunc("POST /grant/unset", mutate(func(a grantReq) error {
		return s.engine.UnsetGrant(a.Role, a.Tag)
	}))

	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		dec, err := s.engine.Authorize(q.Get("subject"), q.Get("instance"), q.Get("action"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, dec)
	})
	mux.HandleFunc("GET /check-tag", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		dec, err := s.engine.CheckTag(q.Get("subject"), q.Get("instance"), q.Get("tag"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, dec)
	})
	mux.HandleFunc("GET /instance/tags", func(w http.ResponseWriter, r *http.Request) {
		tags, err := s.engine.InstanceTags(r.URL.Query().Get("id"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
	})
	mux.HandleFunc("GET /audit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.audit.Events())
	})
	return mux
}

func main() {
	audit := ontology.NewMemoryLogger(4096)
	s := &server{
		engine: ontology.NewEngine(ontology.WithAuditLogger(audit)),
		audit:  audit,
	}
	if err := seed(s.engine); err != nil {
		log.Fatalf("seed: %v", err)
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("ontology server listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, s.routes()))
}
