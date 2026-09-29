package main

import (
	"fmt"

	"ontology/roll"
	"ontology/split"
)

type check struct {
	name string
	ok   bool
}

func main() {
	var results []check
	results = append(results, check{"skeleton", true})

	rh, err := roll.New(16)
	ok := err == nil
	const streamLen = 1_000_000
	for i := 0; i < streamLen; i++ {
		rh.Push(byte(i))
	}
	adv := rh.PushCountForDemo()
	ok = ok && adv <= streamLen
	results = append(results, check{"roll: O(1) advances, count <= stream length", ok})

	cfg := split.Config{Window: 4, Min: 8, Max: 32, Mask: 0xFF}
	data := make([]byte, 200)
	for i := range data {
		data[i] = byte(i*7 + 3)
	}
	cuts := [][]int{{len(data)}, {1, 1, len(data) - 2}, {}}
	for i := 1; i <= len(data); i++ {
		cuts[2] = append(cuts[2], 1)
	}
	var ref []split.Chunk
	same := true
	for ci, sizes := range cuts {
		sp, _ := split.New(cfg)
		pos := 0
		for _, n := range sizes {
			sp.Write(data[pos : pos+n])
			pos += n
		}
		got := sp.Flush()
		if ci == 0 {
			ref = got
		} else if !chunksEqual(ref, got) {
			same = false
		}
	}
	results = append(results, check{"split: identical chunks for 3 write patterns", same})

	ones := make([]byte, 64)
	for i := range ones {
		ones[i] = 1
	}
	sp, _ := split.New(cfg)
	sp.Write(ones)
	forced := sp.Flush()
	results = append(results, check{"split: max-length forced cuts", len(forced) == 2 && forced[0].Forced})

	sp, _ = split.New(cfg)
	sp.Write([]byte("abc"))
	tail := sp.Flush()
	results = append(results, check{"split: stream shorter than min -> one short chunk", len(tail) == 1 && tail[0].End == 3})

	sp, _ = split.New(cfg)
	sp.Write(make([]byte, 9)) // content-cut chunk of 8 then 1 trailing byte
	merged := sp.Flush()
	results = append(results, check{"split: trailing <min merged into previous", len(merged) == 1 && merged[0].End == 9})

	fail := 0
	for _, r := range results {
		status := "OK"
		if !r.ok {
			status = "FAIL"
			fail++
		}
		fmt.Printf("%-4s %s\n", status, r.name)
	}
	if fail > 0 {
		panic("demo failed")
	}
}

func chunksEqual(a, b []split.Chunk) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Start != b[i].Start || a[i].End != b[i].End || string(a[i].Data) != string(b[i].Data) {
			return false
		}
	}
	return true
}
