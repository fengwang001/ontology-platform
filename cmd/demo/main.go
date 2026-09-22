// Command demo exercises the external-sort pipeline end to end and prints
// one OK/FAIL line per acceptance check.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"

	"ontology/merge"
	"ontology/pipeline"
	"ontology/record"
	"ontology/spill"
)

var failures int

func report(ok bool, format string, args ...any) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, fmt.Sprintf(format, args...))
}

func main() {
	root, err := os.MkdirTemp("", "esort-demo")
	if err != nil {
		fmt.Println("FAIL setup:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(root)

	// 1+2: resident bound, spill count, comparison bound, sorted output.
	const n = 50000
	dir := filepath.Join(root, "main")
	p, _ := pipeline.Open(dir, 1<<14)
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("k%07d", (i*2654435761)%500009)
		p.Ingest(key, []byte(fmt.Sprintf("v%d", i)))
	}
	p.Close()
	out := readAll(p.OutputPath())
	sorted := true
	for i := 1; i < len(out); i++ {
		if record.Less(out[i], out[i-1]) {
			sorted = false
		}
	}
	report(p.MaxResident() <= p.Limit() && p.RunCount() >= 2,
		"resident bound: max=%d limit=%d runs=%d", p.MaxResident(), p.Limit(), p.RunCount())
	k := p.RunCount()
	bound := 4 * int64(n) * int64(math.Ceil(math.Log2(float64(k)+1)))
	report(p.Comparisons() <= bound, "comparisons: %d <= bound %d (N=%d K=%d)",
		p.Comparisons(), bound, n, k)
	report(len(out) == n && sorted, "output complete: %d records, sorted=%v", len(out), sorted)

	// 3: equal keys across three runs keep arrival order.
	report(equalKeyOrder(), "equal keys across 3 runs keep arrival order")

	// 4: one example per truncation/corruption class.
	report(truncationClasses(), "truncation classes: header/prefix/body/crc all decidable")

	// 5: crash mid-merge, recover, byte-identical output.
	report(crashRecovery(root), "crash mid-merge: recovered output byte-identical")

	// 6: concurrent ingest conserves record count.
	report(concurrentIngest(root), "concurrent ingest: record count conserved")

	if failures == 0 {
		fmt.Printf("OK total: all checks passed\n")
	} else {
		fmt.Printf("FAIL total: %d check(s) failed\n", failures)
		os.Exit(1)
	}
}

func readAll(path string) []record.Record {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	recs, err := spill.ReadRun(f)
	if err != nil {
		return nil
	}
	return recs
}

func equalKeyOrder() bool {
	mk := func(seqs ...uint64) []record.Record {
		recs := make([]record.Record, len(seqs))
		for i, s := range seqs {
			recs[i] = record.Record{Key: "same", Seq: s}
		}
		return recs
	}
	m := merge.New(
		merge.NewSliceSource(mk(0, 3, 6)),
		merge.NewSliceSource(mk(1, 4, 7)),
		merge.NewSliceSource(mk(2, 5, 8)),
	)
	i := uint64(0)
	ok := true
	m.Merge(func(r record.Record) error {
		if r.Seq != i {
			ok = false
		}
		i++
		return nil
	})
	return ok && i == 9
}

func truncationClasses() bool {
	recs := make([]record.Record, 50)
	for i := range recs {
		recs[i] = record.Record{Key: fmt.Sprintf("k%02d", i), Value: []byte("v"), Seq: uint64(i + 1)}
	}
	var buf bytes.Buffer
	if err := spill.WriteRun(&buf, recs); err != nil {
		return false
	}
	full := buf.Bytes()
	at := func(cut int) error {
		_, err := spill.Recover(bytes.NewReader(full[:cut]))
		return err
	}
	corrupt := append([]byte(nil), full...)
	corrupt[len(corrupt)-1] ^= 0xff
	_, crcErr := spill.Recover(bytes.NewReader(corrupt))
	return errors.Is(at(5), spill.ErrHeaderIncomplete) &&
		errors.Is(at(spill.HeaderSize+2), spill.ErrLengthPrefixIncomplete) &&
		errors.Is(at(spill.HeaderSize+6), spill.ErrRecordBodyIncomplete) &&
		errors.Is(crcErr, spill.ErrCRCMismatch)
}

func crashRecovery(root string) bool {
	ingest := func(dir string, cp pipeline.CrashPoint) error {
		p, err := pipeline.Open(dir, 4096)
		if err != nil {
			return err
		}
		for i := 0; i < 3000; i++ {
			p.Ingest(fmt.Sprintf("key%06d", (i*7919)%5000), []byte("v"))
		}
		p.SetCrashPoint(cp)
		return p.Close()
	}
	ref := filepath.Join(root, "ref")
	if err := ingest(ref, pipeline.CrashNone); err != nil {
		return false
	}
	crashDir := filepath.Join(root, "crash")
	if err := ingest(crashDir, pipeline.CrashMidMerge); !errors.Is(err, pipeline.ErrCrashed) {
		return false
	}
	p2, err := pipeline.Open(crashDir, 4096)
	if err != nil || p2.Close() != nil {
		return false
	}
	want, err1 := os.ReadFile(filepath.Join(ref, "output.osrt"))
	got, err2 := os.ReadFile(p2.OutputPath())
	return err1 == nil && err2 == nil && bytes.Equal(want, got)
}

func concurrentIngest(root string) bool {
	p, err := pipeline.Open(filepath.Join(root, "conc"), 1<<16)
	if err != nil {
		return false
	}
	const g, per = 4, 5000
	var wg sync.WaitGroup
	ok := true
	for w := 0; w < g; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				if p.Ingest(fmt.Sprintf("key%07d", w*per+i), []byte("v")) != nil {
					ok = false
				}
			}
		}(w)
	}
	wg.Wait()
	if p.Close() != nil {
		return false
	}
	return ok && len(readAll(p.OutputPath())) == g*per &&
		p.MaxResident() <= p.Limit()
}
