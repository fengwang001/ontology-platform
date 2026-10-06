// Command server 提供源码映射登记/合成/查询的最小 HTTP 服务。
//
// 接口（均为 POST，JSON）：
//
//	POST /register {"name":"m","sourceCount":1,"lines":[...]}
//	POST /compose  {"m2":"final-to-mid","m1":"mid-to-src","result":"m3"}
//	POST /query    {"name":"m3","line":0,"column":3}
//
// 查询成功返回 {"mapped":true,"sourceIndex":0,"line":..,"column":..}，
// 未映射返回 {"mapped":false}。错误返回 4xx 并带 {"errorCategory":..,"message":..}。
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"ontology/srcmap"
)

func main() {
	handler := NewHandler(srcmap.NewService())
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("srcmap server listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, handler))
}

// NewHandler 构建全部路由，便于进程内测试。
func NewHandler(svc *srcmap.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name        string        `json:"name"`
			SourceCount int           `json:"sourceCount"`
			Lines       []srcmap.Line `json:"lines"`
		}
		if !decode(w, r, &req) {
			return
		}
		log.Printf("register name=%q sourceCount=%d lines=%d", req.Name, req.SourceCount, len(req.Lines))
		writeErr(w, svc.Register(req.Name, req.SourceCount, req.Lines))
	})
	mux.HandleFunc("/compose", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			M2     string `json:"m2"`
			M1     string `json:"m1"`
			Result string `json:"result"`
		}
		if !decode(w, r, &req) {
			return
		}
		log.Printf("compose m2=%q m1=%q result=%q", req.M2, req.M1, req.Result)
		writeErr(w, svc.Compose(req.M2, req.M1, req.Result))
	})
	mux.HandleFunc("/query", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name   string `json:"name"`
			Line   int    `json:"line"`
			Column int    `json:"column"`
		}
		if !decode(w, r, &req) {
			return
		}
		log.Printf("query name=%q line=%d column=%d", req.Name, req.Line, req.Column)
		res, err := svc.Query(req.Name, req.Line, req.Column)
		if err != nil {
			writeErr(w, err)
			return
		}
		out := map[string]any{"mapped": res.Mapped}
		if res.Mapped {
			out["sourceIndex"] = res.Position.SourceIndex
			out["line"] = res.Position.Line
			out["column"] = res.Position.Column
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &req) {
			return
		}
		sc, lines, err := svc.Get(req.Name)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sourceCount": sc, "lines": lines})
	})

	return mux
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"errorCategory": string(srcmap.CategoryInvalidArgument), "message": "POST required"})
		return false
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"errorCategory": string(srcmap.CategoryInvalidArgument), "message": err.Error()})
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	status := http.StatusBadRequest
	switch srcmap.CategoryOf(err) {
	case srcmap.CategoryNotFound:
		status = http.StatusNotFound
	case srcmap.CategoryDuplicate:
		status = http.StatusConflict
	case srcmap.CategoryPositionOverflow:
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, map[string]string{
		"errorCategory": string(srcmap.CategoryOf(err)),
		"message":       err.Error(),
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
