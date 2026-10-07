package main

import (
	"encoding/json"
	"log"
	"net/http"

	"ontology-platform/ontology"
)

// 这是一个可运行的最小演示服务：注册带基数约束的链接类型，
// 并通过 /update 暴露带有限次重试的乐观提交。
func main() {
	store := ontology.NewStore()
	must(store.RegisterLinkType(ontology.LinkType{
		ID:           "owns",
		CardinalityA: &ontology.Cardinality{Max: 1},
	}))
	must(store.CreateInstance("x"))
	for i := 0; i < 8; i++ {
		must(store.CreateInstance("y" + itoa(i)))
	}

	engine := ontology.NewEngine(store, ontology.DefaultMaxAttempts, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/update", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var dto ontology.UpdateRequestDTO
		if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req, err := ontology.ParseUpdate(dto)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		res, err := engine.Update(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		body, _ := ontology.MarshalResponse(res)
		switch {
		case res.Committed:
			w.WriteHeader(http.StatusOK)
		case res.Reject.Code == ontology.CodeVersionConflict:
			w.WriteHeader(http.StatusConflict)
		case res.Reject.Code == ontology.CodeCardinality:
			w.WriteHeader(http.StatusUnprocessableEntity)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})

	addr := ":8080"
	log.Printf("ontology server listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
