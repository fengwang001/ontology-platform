package pipeline

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/spill"
)

// 三个崩溃点：Spill 完成后、Merge 中途、Finalize 之前。
// 恢复后的最终输出必须与不崩溃时逐字节相同。
func TestCrashRecoveryByteIdentical(t *testing.T) {
	const n = 500
	keyFn := func(i int) string { return fmt.Sprintf("%08x", uint32(i)*2654435761) }
	refDir := t.TempDir()
	ref, err := Open(refDir, 2048, nil)
	if err != nil {
		t.Fatal(err)
	}
	ingestN(t, ref, n, keyFn)
	if err := ref.Close(); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(ref.OutputPath())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		point CrashPoint
		skip  int // 第 skip+1 次到达该崩溃点时注入
	}{
		{"after spill", CrashAfterSpill, 0},
		{"mid merge", CrashDuringMerge, 5},
		{"before finalize", CrashBeforeFinalize, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			calls := 0
			hook := func(pt CrashPoint) error {
				if pt != tc.point {
					return nil
				}
				calls++
				if calls > tc.skip {
					return errors.New("boom")
				}
				return nil
			}
			p, err := Open(dir, 2048, &Options{CrashHook: hook})
			if err != nil {
				t.Fatal(err)
			}
			ingestN(t, p, n, keyFn)
			if err := p.Close(); !errors.Is(err, ErrCrashInjected) {
				t.Fatalf("want ErrCrashInjected, got %v", err)
			}
			// 模拟崩溃：丢弃旧实例，从磁盘恢复。
			rec, err := Open(dir, 2048, nil)
			if err != nil {
				t.Fatalf("recover: %v", err)
			}
			got, err := os.ReadFile(rec.OutputPath())
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("recovered output differs from crash-free output (%d vs %d bytes)",
					len(got), len(want))
			}
		})
	}
}

// 并发 Ingest：不丢、不重、驻留上界仍成立；Close 后的 Ingest 返回 ErrClosed。
func TestConcurrentIngestAndClose(t *testing.T) {
	p, err := Open(t.TempDir(), 64*1024, nil)
	if err != nil {
		t.Fatal(err)
	}
	const goroutines = 8
	const perG = 5000
	var okCount atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				key := fmt.Sprintf("k%08d", (g*perG+i)%9973)
				err := p.Ingest(key, []byte("v"))
				if errors.Is(err, ErrClosed) {
					return
				}
				if err != nil {
					t.Errorf("Ingest: %v", err)
					return
				}
				okCount.Add(1)
			}
		}(g)
	}
	time.Sleep(2 * time.Millisecond)
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wg.Wait()
	if err := p.Ingest("late", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Ingest after Close: want ErrClosed, got %v", err)
	}
	out, err := spill.ReadAll(p.OutputPath())
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(out)) != okCount.Load() {
		t.Fatalf("output %d records, but %d successful ingests", len(out), okCount.Load())
	}
	seen := make(map[uint64]bool, len(out))
	for _, rec := range out {
		if seen[rec.Seq] {
			t.Fatalf("duplicate seq %d", rec.Seq)
		}
		seen[rec.Seq] = true
	}
	assertSorted(t, out)
	if p.MaxResident() > p.ResidentLimit() {
		t.Fatalf("max resident %d exceeds limit %d", p.MaxResident(), p.ResidentLimit())
	}
}
