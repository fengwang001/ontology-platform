package main

import (
	"fmt"
	"path/filepath"

	"ontology/wal"

	"ontology/req"
)

type check struct {
	name string
	fn   func() bool
}

func main() {
	checks := []check{
		{"req roundtrip", func() bool {
			r := req.New(nil)
			r.Resolve(req.Result{Seq: 7})
			res := <-r.Done()
			return res.Seq == 7
		}},
		{"wal roundtrip+atomic", func() bool {
			path := tmpLog()
			w, err := wal.Create(path, wal.Options{})
			if err != nil {
				return false
			}
			w.Write(wal.Batch{SeqStart: 1, Payloads: [][]byte{[]byte("a"), []byte("b"), []byte("c")}})
			w.Write(wal.Batch{SeqStart: 4, Payloads: [][]byte{[]byte("d")}})
			w.Close()
			got, err := wal.ReadBatches(path)
			return err == nil && len(got) == 2 && got[1].SeqStart == 4
		}},
	}
	pass := 0
	for _, c := range checks {
		if c.fn() {
			fmt.Printf("OK %s\n", c.name)
			pass++
		} else {
			fmt.Printf("FAIL %s\n", c.name)
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("FAIL")
	}
}

func tmpLog() string {
	d, _ := os.MkdirTemp("", "demo-*")
	return filepath.Join(d, "log.wal")
}
