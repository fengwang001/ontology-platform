// Command demo 端到端演示可崩溃恢复的外部排序管线：驻留上界、比较次数上界、
// 等键到达顺序、逐字节截断分类、崩溃恢复逐字节一致、并发守恒。
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"ontology/merge"
	"ontology/pipeline"
	"ontology/record"
	"ontology/spill"
)

var failures int

func report(ok bool, format string, args ...any) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, fmt.Sprintf(format, args...))
}

func keyOf(i int) string { return fmt.Sprintf("%08x", uint32(i)*2654435761) }

func checkTruncation(dir string) {
	recs := make([]record.Record, 50)
	for i := range recs {
		recs[i] = record.Record{Key: fmt.Sprintf("k%03d", i), Value: []byte{1, 2, 3, 4}, Seq: uint64(i)}
	}
	src := filepath.Join(dir, "trunc.run")
	if err := spill.WriteRun(src, recs); err != nil {
		report(false, "write run: %v", err)
		return
	}
	data, _ := os.ReadFile(src)
	examples := []struct {
		name string
		off  int
		want error
	}{
		{"header incomplete", 10, spill.ErrHeaderIncomplete},
		{"length prefix incomplete", 22, spill.ErrLengthPrefixIncomplete},
		{"record body incomplete", 29, spill.ErrRecordBodyIncomplete},
		{"crc mismatch", 49, spill.ErrCRCMismatch},
	}
	for _, ex := range examples {
		path := filepath.Join(dir, "t.run")
		os.WriteFile(path, data[:ex.off], 0o644)
		got, err := spill.RecoverPrefix(path)
		report(errors.Is(err, ex.want), "truncate off=%d -> %v (recovered prefix=%d)", ex.off, ex.want, len(got))
	}
}

func checkCrashRecovery(dir string) {
	keyFn := func(i int) string { return fmt.Sprintf("%08x", uint32(i)*2654435761) }
	ref, _ := pipeline.Open(filepath.Join(dir, "ref"), 2048, nil)
	for i := 0; i < 500; i++ {
		ref.Ingest(keyFn(i), []byte("payload!"))
	}
	ref.Close()
	want, _ := os.ReadFile(ref.OutputPath())
	points := []struct {
		name string
		pt   pipeline.CrashPoint
		skip int
	}{
		{"after-spill", pipeline.CrashAfterSpill, 0},
		{"mid-merge", pipeline.CrashDuringMerge, 5},
		{"before-finalize", pipeline.CrashBeforeFinalize, 0},
	}
	for _, pc := range points {
		calls := 0
		hook := func(pt pipeline.CrashPoint) error {
			if pt != pc.pt {
				return nil
			}
			calls++
			if calls > pc.skip {
				return errors.New("boom")
			}
			return nil
		}
		p, _ := pipeline.Open(filepath.Join(dir, pc.name), 2048, &pipeline.Options{CrashHook: hook})
		for i := 0; i < 500; i++ {
			p.Ingest(keyFn(i), []byte("payload!"))
		}
		crashed := errors.Is(p.Close(), pipeline.ErrCrashInjected)
		rec, err := pipeline.Open(filepath.Join(dir, pc.name), 2048, nil)
		got, rerr := os.ReadFile(rec.OutputPath())
		ok := crashed && err == nil && rerr == nil && string(got) == string(want)
		report(ok, "crash at %s: recovered output byte-identical (%d bytes)", pc.name, len(got))
	}
}

func checkConcurrency(dir string) {
	p, _ := pipeline.Open(filepath.Join(dir, "conc"), 64*1024, nil)
	var okCount atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				if err := p.Ingest(fmt.Sprintf("k%08d", (g*2000+i)%997), []byte("v")); err != nil {
					return
				}
				okCount.Add(1)
			}
		}(g)
	}
	wg.Wait()
	p.Close()
	out, err := spill.ReadAll(p.OutputPath())
	ok := err == nil && int64(len(out)) == okCount.Load() && p.MaxResident() <= p.ResidentLimit()
	report(ok, "concurrent ingest: %d records conserved, max resident %d <= %d",
		len(out), p.MaxResident(), p.ResidentLimit())
}

func main() {
	dir, err := os.MkdirTemp("", "extsort-demo")
	if err != nil {
		fmt.Println("FAIL", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	const n = 20000
	p, err := pipeline.Open(filepath.Join(dir, "main"), 128*1024, nil)
	if err != nil {
		fmt.Println("FAIL", err)
		os.Exit(1)
	}
	for i := 0; i < n; i++ {
		p.Ingest(keyOf(i), []byte("payload!"))
	}
	p.Close()
	out, _ := spill.ReadAll(p.OutputPath())
	sorted := len(out) == n
	for i := 1; i < len(out); i++ {
		if out[i-1].Key > out[i].Key {
			sorted = false
		}
	}
	report(p.MaxResident() <= p.ResidentLimit(),
		"resident bound: max=%d limit=%d runs=%d", p.MaxResident(), p.ResidentLimit(), p.RunCount())
	bound := merge.Bound(int64(n), int64(p.RunCount()))
	report(p.Compares() <= bound, "merge compares: %d <= bound %d (N=%d K=%d)",
		p.Compares(), bound, n, p.RunCount())
	report(sorted, "output integrity: %d records, keys non-decreasing", len(out))

	eq, _ := pipeline.Open(filepath.Join(dir, "eq"), 280, nil)
	for i := 0; i < 25; i++ {
		eq.Ingest("x", []byte("payload!"))
	}
	eq.Close()
	eqOut, _ := spill.ReadAll(eq.OutputPath())
	arrival := len(eqOut) == 25 && eq.RunCount() >= 3
	for i, rec := range eqOut {
		if rec.Seq != uint64(i) {
			arrival = false
		}
	}
	report(arrival, "equal keys across %d runs: arrival order preserved", eq.RunCount())

	os.MkdirAll(filepath.Join(dir, "trunc"), 0o755)
	checkTruncation(filepath.Join(dir, "trunc"))
	checkCrashRecovery(dir)
	checkConcurrency(dir)

	if failures > 0 {
		fmt.Printf("FAIL total: %d check(s) failed\n", failures)
		os.Exit(1)
	}
	fmt.Println("OK   total: all checks passed")
}
