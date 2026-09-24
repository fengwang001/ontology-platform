package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ontology/event"
	"ontology/segment"
)

type check struct {
	name string
	ok   bool
}

func (c check) line() string {
	if c.ok {
		return "OK " + c.name
	}
	return "FAIL " + c.name
}

func main() {
	checks := []check{
		{"event encode/crc", eventOK()},
		{"segment truncation classes", segmentOK()},
	}
	pass := 0
	for _, c := range checks {
		fmt.Println(c.line())
		if c.ok {
			pass++
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo failed")
	}
}

func eventOK() bool {
	raw, err := event.Encode(nil, []byte("demo"))
	if err != nil {
		return false
	}
	body, n, err := event.Consume(raw)
	return err == nil && n == len(raw) && string(body) == "demo"
}

func segmentOK() (ok bool) {
	dir, err := os.MkdirTemp("", "segdemo")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)
	w, err := segment.Create(filepath.Join(dir, "s.log"), 0)
	if err != nil {
		return false
	}
	for i := 0; i < 500; i++ {
		if err := w.Append([]byte{byte(i), byte(i >> 8)}); err != nil {
			return false
		}
	}
	if err := w.Close(); err != nil {
		return false
	}
	full, err := os.ReadFile(filepath.Join(dir, "s.log"))
	if err != nil {
		return false
	}
	want := map[int]error{1: segment.ErrTruncatedHeader, 29: segment.ErrTruncatedLength,
		33: segment.ErrTruncatedBody, 40: segment.ErrTruncatedCRC}
	for size, e := range want {
		p := filepath.Join(dir, "c.log")
		if err := os.WriteFile(p, full[:size], 0o644); err != nil {
			return false
		}
		rep, _ := segment.Inspect(p)
		if !errors.Is(rep.Err, e) {
			return false
		}
	}
	return true
}
