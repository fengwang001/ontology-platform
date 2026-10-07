// server 提供一个最小 HTTP 演示服务，把嵌套动作引擎暴露为接口。
package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"ontology/ontology"
)

type server struct {
	eng   *ontology.Engine
	store *ontology.Store
}

type eventDTO struct {
	Path     string `json:"path"`
	Depth    int    `json:"depth"`
	Action   string `json:"action"`
	Critical bool   `json:"critical"`
	Phase    string `json:"phase"`
	Outcome  string `json:"outcome"`
	Detail   string `json:"detail,omitempty"`
}

type executeResponse struct {
	Committed  bool           `json:"committed"`
	Aborted    bool           `json:"aborted"`
	Reason     string         `json:"reason,omitempty"`
	CommitSeq  uint64         `json:"commitSeq,omitempty"`
	Outputs    map[string]any `json:"outputs,omitempty"`
	Conclusion string         `json:"conclusion"`
	Events     []eventDTO     `json:"events"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) handleExecute(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var args ontology.Args
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil && r.Body != http.NoBody {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res := s.eng.Execute(name, args)
	resp := executeResponse{
		Committed:  res.Committed,
		Aborted:    res.Aborted,
		Reason:     res.Reason,
		CommitSeq:  res.CommitSeq,
		Outputs:    res.Outputs,
		Conclusion: res.Trace.Conclusion(),
	}
	for _, e := range res.Trace.Events() {
		resp.Events = append(resp.Events, eventDTO{
			Path: e.Path, Depth: e.Depth, Action: e.Action, Critical: e.Critical,
			Phase: string(e.Phase), Outcome: e.Outcome.String(), Detail: e.Detail,
		})
	}
	status := http.StatusOK
	if res.Aborted {
		status = http.StatusConflict
	}
	writeJSON(w, status, resp)
}

func (s *server) handleObject(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/objects/")
	obj, ok := s.store.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, obj)
}

func (s *server) handleCommits(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.eng.CommitLog())
}

// demoRegistry 构造演示用动作：transfer 关键调用 audit、非关键调用 notify。
func demoRegistry() *ontology.Registry {
	reg := ontology.NewRegistry()
	must := func(a *ontology.Action) {
		if err := reg.Register(a); err != nil {
			log.Fatalf("register %s: %v", a.Name, err)
		}
	}
	must(&ontology.Action{
		Name: "audit",
		Run: func(ctx *ontology.Context) error {
			ctx.Put(ontology.Object{ID: "audit-log", Type: "log",
				Props: map[string]any{"msg": "transfer audited"}})
			return nil
		},
	})
	must(&ontology.Action{
		Name: "notify",
		Run: func(ctx *ontology.Context) error {
			ctx.Put(ontology.Object{ID: "note", Type: "note",
				Props: map[string]any{"text": "transfer done"}})
			return nil
		},
	})
	must(&ontology.Action{
		Name: "transfer",
		Preconditions: []ontology.Condition{{
			Name: "source-exists",
			Check: func(v *ontology.View) error {
				if _, ok := v.Get("acc"); !ok {
					return errors.New("account acc does not exist")
				}
				return nil
			},
		}},
		Calls: []ontology.CallSpec{
			{Action: "audit", Critical: true},
			{Action: "notify", Critical: false},
		},
		Run: func(ctx *ontology.Context) error {
			obj, _ := ctx.Get("acc")
			amount, ok := ctx.Arg("amount").(float64) // JSON 数字解码为 float64
			if !ok {
				return errors.New("amount must be a number")
			}
			balance := obj.Props["balance"].(int) - int(amount)
			ctx.Put(ontology.Object{ID: "acc", Type: "account",
				Props: map[string]any{"balance": balance}})
			ctx.Invoke("audit", ontology.Args{"kind": "transfer"}, true)
			ctx.Invoke("notify", ontology.Args{"to": "ops"}, false)
			ctx.SetOutput("balance", balance)
			return nil
		},
	})
	return reg
}

func newMux(s *server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/actions/{name}/execute", s.handleExecute)
	mux.HandleFunc("GET /v1/objects/{id}", s.handleObject)
	mux.HandleFunc("GET /v1/commits", s.handleCommits)
	return mux
}

func demoServer() *server {
	store := ontology.NewStore()
	store.Seed(ontology.Object{ID: "acc", Type: "account", Props: map[string]any{"balance": 100}})
	store.RegisterInvariant("account", func(obj ontology.Object) error {
		if obj.Props["balance"].(int) < 0 {
			return errors.New("balance must stay non-negative")
		}
		return nil
	})
	eng, err := ontology.NewEngine(demoRegistry(), store)
	if err != nil {
		log.Fatalf("engine: %v", err)
	}
	return &server{eng: eng, store: store}
}

func main() {
	s := demoServer()
	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", newMux(s)))
}
