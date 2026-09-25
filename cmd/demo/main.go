package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ontology/event"
	"ontology/repair"
	"ontology/replay"
	"ontology/segment"
	"ontology/sparse"
)

var failed int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s\n", status, name)
}

func must(err error) {
	if err != nil {
		fmt.Println("FAIL setup:", err)
		os.Exit(1)
	}
}

func seqsOK(evs []event.Event, from uint64) bool {
	for i, e := range evs {
		if e.Seq != from+uint64(i) {
			return false
		}
	}
	return true
}

func main() {
	dir, err := os.MkdirTemp("", "ontology-demo")
	must(err)
	defer os.RemoveAll(dir)

	l, err := replay.Open(filepath.Join(dir, "log"), 4, 16)
	must(err)
	for i := 0; i < 48; i++ {
		_, err = l.Append([]byte{byte(i)})
		must(err)
	}
	must(l.Close())

	ev1, c1, err := l.Replay(16, 19)
	must(err)
	check("locate from==anchor", seqsOK(ev1, 16) && len(ev1) == 4 && c1.Skipped == 0)
	ev2, c2, err := l.Replay(18, 20)
	must(err)
	check("locate from between anchors", seqsOK(ev2, 18) && c2.Skipped == 2)
	ev3, _, err := l.Replay(0, 2)
	must(err)
	check("locate from below first anchor", seqsOK(ev3, 0) && len(ev3) == 3)

	big, err := replay.Open(filepath.Join(dir, "big"), 128, 100000)
	must(err)
	for i := 0; i < 100000; i++ {
		_, err = big.Append([]byte{byte(i)})
		must(err)
	}
	must(big.Close())
	_, cb, err := big.Replay(77777, 77778)
	must(err)
	check("skipped < anchor interval N=128", cb.Skipped < 128)

	seg0 := filepath.Join(dir, "log", "seg-000000.log")
	idx0 := seg0 + ".idx"
	orig, err := os.ReadFile(idx0)
	must(err)
	must(os.Remove(idx0))
	must(repair.RebuildIndex(seg0))
	rebuilt, err := os.ReadFile(idx0)
	must(err)
	check("index rebuild byte-identical", string(orig) == string(rebuilt))

	evx, _, err := l.Replay(3, 40)
	must(err)
	check("replay across 3 segments no dup/gap", seqsOK(evx, 3) && len(evx) == 38)

	full, err := os.ReadFile(seg0)
	must(err)
	recLen := 4 + event.EncodedLen(1) + 4
	trunc := func(n int) error {
		p := filepath.Join(dir, "trunc.log")
		must(os.WriteFile(p, full[:n], 0o644))
		return repair.Classify(p)
	}
	cut := segment.HeaderSize + 5*recLen
	check("classify header incomplete", errors.Is(trunc(10), segment.ErrHeaderIncomplete))
	check("classify length prefix incomplete", errors.Is(trunc(cut+2), segment.ErrLengthPrefixIncomplete))
	check("classify event body incomplete", errors.Is(trunc(cut+9), segment.ErrEventBodyIncomplete))
	bad := append([]byte(nil), full...)
	bad[segment.HeaderSize+6] ^= 0xFF
	must(os.WriteFile(filepath.Join(dir, "flip.log"), bad, 0o644))
	check("classify crc mismatch",
		errors.Is(repair.Classify(filepath.Join(dir, "flip.log")), segment.ErrCRCMismatch))

	p := filepath.Join(dir, "torepair.log")
	must(os.WriteFile(p, full[:cut+9], 0o644))
	before, after, err := repair.RepairSegment(p)
	must(err)
	check("repair fixes header count", before == 16 && after == 5 && repair.Classify(p) == nil)

	idx, err := sparse.ReadFile(idx0)
	must(err)
	idx.Anchors[2].Offset += 5
	must(sparse.WriteFile(idx0, idx))
	ev9, c9, err := l.Replay(8, 15)
	must(err)
	check("tampered index falls back correct+flagged", seqsOK(ev9, 8) && len(ev9) == 8 && c9.IndexInvalid)
	must(repair.RebuildIndex(seg0))

	gapDir := filepath.Join(dir, "gap")
	must(os.MkdirAll(gapDir, 0o755))
	w, err := segment.Create(filepath.Join(gapDir, "a.log"), 4, 0)
	must(err)
	for i := uint64(0); i < 10; i++ {
		must(w.Append(event.Encode(event.Event{Seq: i})))
	}
	must(w.Close())
	w, err = segment.Create(filepath.Join(gapDir, "b.log"), 4, 12)
	must(err)
	for i := uint64(12); i < 17; i++ {
		must(w.Append(event.Encode(event.Event{Seq: i})))
	}
	must(w.Close())
	gaps, gerr := repair.CheckGaps([]string{
		filepath.Join(gapDir, "a.log"), filepath.Join(gapDir, "b.log")})
	check("sequence gap detected", errors.Is(gerr, repair.ErrSeqGap) &&
		len(gaps) == 1 && gaps[0].From == 10 && gaps[0].To == 11)

	fmt.Printf("TOTAL %d/13 passed\n", 13-failed)
	if failed > 0 {
		os.Exit(1)
	}
}
