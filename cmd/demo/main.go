// Command demo runs the external-sort acceptance checks end to end in
// a local temp directory: no flags, no network.
package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"ontology/pipeline"
	"ontology/record"
	"ontology/spill"
)

func main() {
	maybeCrashChild()
	root, err := os.MkdirTemp("", "esort-demo-")
	must(err)
	defer os.RemoveAll(root)

	n, fails := 20000, 0
	ok := func(cond bool, msg string, args ...any) {
		line := "OK  "
		if !cond {
			line, fails = "FAIL", fails+1
		}
		fmt.Println(line, fmt.Sprintf(msg, args...))
	}

	dir := filepath.Join(root, "main")
	p, err := pipeline.Open(pipeline.Config{Dir: dir, MemoryByte: 64 << 10})
	must(err)
	for i := 0; i < n; i++ {
		if i%7 == 0 {
			_, err = p.Ingest("dup", enc(uint32(i)))
		} else {
			_, err = p.Ingest(fmt.Sprintf("k%07d", n-1-i), []byte{byte(i)})
		}
		must(err)
	}
	must(p.Close())
	out, err := p.ReadOutput()
	must(err)
	st := p.Stats()
	ok(len(out) == n, "output %d records == ingested %d", len(out), n)
	ok(st.PeakResident <= st.Limit, "resident peak %d <= limit %d", st.PeakResident, st.Limit)
	bound := uint64(4 * float64(n) * math.Ceil(math.Log2(float64(st.Runs+1))))
	ok(st.Comparisons <= bound, "comparisons %d <= bound %d (K=%d)", st.Comparisons, bound, st.Runs)
	ok(fifoOK(out), "equal-key FIFO preserved across %d runs", st.Runs)

	rs := []record.Record{{Key: "abc", Value: make([]byte, 40), Seq: 1}}
	c1 := probeRun(filepath.Join(root, "c1"), rs, 7)
	c2 := probeRun(filepath.Join(root, "c2"), rs, spill.HeaderSize+2)
	c3 := probeRun(filepath.Join(root, "c3"), rs, spill.HeaderSize+6)
	c4dir := filepath.Join(root, "c4")
	c4 := probeRun(c4dir, rs, -1)
	flipByte(spill.RunPath(c4dir, 1), spill.HeaderSize+10)
	c4 = classify(spill.RunPath(c4dir, 1))
	ok(c1 == "header-incomplete" && c2 == "length-incomplete" &&
		c3 == "record-incomplete" && c4 == "crc-mismatch",
		"truncation classes header/len/body/crc = %s/%s/%s/%s", c1, c2, c3, c4)

	want := runBaseline(filepath.Join(root, "base"))
	crashOK := true
	for _, phase := range []string{"spill", "merge", "finalize"} {
		rd := filepath.Join(root, "crash-"+phase)
		runCrashChild(rd, phase)
		q, err := pipeline.Open(pipeline.Config{Dir: rd, MemoryByte: 4096})
		must(err)
		must(q.Close())
		got, err := os.ReadFile(q.OutputPath())
		must(err)
		crashOK = crashOK && sha256.Sum256(got) == sha256.Sum256(want)
	}
	ok(crashOK, "spill/merge/finalize crashes recover byte-identical")

	cdir := filepath.Join(root, "conc")
	cp, err := pipeline.Open(pipeline.Config{Dir: cdir, MemoryByte: 16 << 10})
	must(err)
	done := make(chan struct{}, 8)
	for w := 0; w < 8; w++ {
		go func(w int) {
			for i := 0; i < 1000; i++ {
				_, _ = cp.Ingest(fmt.Sprintf("k%07d", w*1000+i), []byte{byte(w)})
			}
			done <- struct{}{}
		}(w)
	}
	for w := 0; w < 8; w++ {
		<-done
	}
	must(cp.Close())
	cs, err := cp.ReadOutput()
	must(err)
	ok(len(cs) == 8000 && cp.Stats().PeakResident <= cp.Stats().Limit,
		"concurrent ingest conserved %d/8000 records, peak within limit", len(cs))

	fmt.Printf("TOTAL failures=%d\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}

func must(err error) {
	if err != nil {
		fmt.Println("FAIL", err)
		os.Exit(1)
	}
}

func enc(v uint32) []byte {
	return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}

func fifoOK(rs []record.Record) bool {
	var expect uint32
	for _, r := range rs {
		if r.Key != "dup" {
			continue
		}
		v := dec(r.Value)
		if v != expect {
			return false
		}
		expect += 7
	}
	return true
}

func errorIs(err, target error) bool { return errors.Is(err, target) }
