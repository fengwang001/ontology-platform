package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"ontology/ontology"
)

// ---- request payloads ----

type idReq struct {
	ID string `json:"id"`
}

type linkTypeReq struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

type roleReq struct {
	ID      string   `json:"id"`
	Parents []string `json:"parents"`
}

type subjectReq struct {
	ID    string   `json:"id"`
	Roles []string `json:"roles"`
}

type instanceReq struct {
	ID         string `json:"id"`
	ObjectType string `json:"objectType"`
}

type attachReq struct {
	Tag        string `json:"tag"`
	ObjectType string `json:"objectType"`
}

type propagationReq struct {
	Tag       string `json:"tag"`
	LinkType  string `json:"linkType"`
	Direction string `json:"direction"` // "downstream" (default) or "upstream"
}

type pairReq struct {
	Tag      string `json:"tag"`
	LinkType string `json:"linkType"`
}

type grantReq struct {
	Role   string `json:"role"`
	Tag    string `json:"tag"`
	Effect string `json:"effect"` // "allow" (default) or "deny"
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var de *ontology.DecisionError
	if errors.As(err, &de) {
		status = http.StatusForbidden
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// mutate decodes the request body into A and runs a mutation.
func mutate[A any](fn func(A) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var a A
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
			fail(w, err)
			return
		}
		if err := fn(a); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func parseDirection(s string) (ontology.Direction, error) {
	switch s {
	case "", "downstream":
		return ontology.Downstream, nil
	case "upstream":
		return ontology.Upstream, nil
	}
	return ontology.Downstream, fmt.Errorf("unknown direction %q", s)
}

func parseEffect(s string) (ontology.Effect, error) {
	switch s {
	case "", "allow":
		return ontology.Allow, nil
	case "deny":
		return ontology.Deny, nil
	}
	return ontology.Allow, fmt.Errorf("unknown effect %q", s)
}

// seed installs a demo scenario: confidential tags on folders propagate
// downstream to contained documents; admins may read them, readers may not.
func seed(e *ontology.Engine) error {
	steps := []func() error{
		func() error { return e.DeclareObjectType("Folder") },
		func() error { return e.DeclareObjectType("Document") },
		func() error { return e.DeclareLinkType("contains", "Folder", "Document") },
		func() error { return e.DeclareTag("confidential") },
		func() error { return e.AttachTag("confidential", "Folder") },
		func() error {
			return e.DeclarePropagation("confidential", "contains", ontology.Downstream)
		},
		func() error { return e.DeclareRole("admin") },
		func() error { return e.DeclareRole("reader") },
		func() error { return e.DeclareSubject("alice", "reader") },
		func() error { return e.DeclareSubject("bob", "admin") },
		func() error { return e.SetGrant("admin", "confidential", ontology.Allow) },
		func() error { return e.DeclareInstance("folder-1", "Folder") },
		func() error { return e.DeclareInstance("doc-1", "Document") },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}
