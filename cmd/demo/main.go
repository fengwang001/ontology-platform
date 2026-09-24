package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"ontology/event"
	"ontology/segment"
	"ontology/sparse"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
		return
	}
	fmt.Println("FAIL", name)
	fails++
}

func main() {
	dir, err := os.MkdirTemp("", "ontology-demo-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	// Build one segment with 600 events, capturing anchors every 128 frames.
	sp := filepath.Join(dir, "seg-0000")
	w, err := segment.Create(sp, 0)
	if err != nil {
		panic(err)
	}
	idx := &sparse.Index{Every: 128}
	for i := 0; i < 600; i++ {
		off, _ := w.SeekOffset()
		if err := w.Append(event.Event{Seq: uint64(i), Payload: []byte("p")}); err != nil {
			panic(err)
		}
		idx.Add(i, uint64(i), uint64(off))
	}
	w.Close()
	raw, _ := os.ReadFile(sp)

	// Judgment: rebuild index from segment bytes, identical byte for byte.
	reb, err := sparse.Rebuild(raw, 128)
	if err != nil {
		panic(err)
	}
	check("rebuild index byte-identical", bytes.Equal(reb.Marshal(), idx.Marshal()))

	if fails == 0 {
		fmt.Println("TOTAL OK (1/8 judgments)")
	} else {
		fmt.Printf("TOTAL FAIL %d\n", fails)
		os.Exit(1)
	}
}
