package main

import (
	"errors"
	"fmt"
	"ontology/level"
	"ontology/segment"
	"ontology/verify"
	"os"
	"path/filepath"
)

func tmp() string {
	d, err := os.MkdirTemp("", "lsmdemo")
	if err != nil {
		panic(err)
	}
	return d
}

func writeSeg(kvs ...[3]string) string {
	p := filepath.Join(tmp(), "w.seg")
	w, err := segment.NewWriter(p)
	if err != nil {
		panic(err)
	}
	for _, e := range kvs {
		if e[2] == "del" {
			err = w.Delete([]byte(e[0]))
		} else {
			err = w.Add([]byte(e[0]), []byte(e[1]))
		}
		if err != nil {
			panic(err)
		}
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return p
}

func writeSegN(n int, salt ...int) string {
	p := filepath.Join(tmp(), "n.seg")
	w, _ := segment.NewWriter(p)
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("key%08d", i)
		if len(salt) > 0 {
			k = fmt.Sprintf("key%08d", i*5+salt[0])
		}
		if err := w.Add([]byte(k), []byte("v")); err != nil {
			panic(err)
		}
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return p
}

func openStore() *level.Store {
	s, err := level.OpenStore(tmp())
	if err != nil {
		panic(err)
	}
	return s
}

func ingest(s *level.Store, lvl int, kvs ...[3]string) {
	if _, err := s.Ingest(writeSeg(kvs...), lvl); err != nil {
		panic(err)
	}
}

func get(s *level.Store, key string) (string, segment.State) {
	v, st, err := s.Get([]byte(key))
	if err != nil {
		panic(err)
	}
	return string(v), st
}

func truncClass(seg string, full []byte, n int, want error) bool {
	if err := os.WriteFile(seg, full[:n], 0o644); err != nil {
		return false
	}
	return errors.Is(verify.CheckSegment(seg), want)
}
