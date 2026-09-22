package pipeline

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func runCrashed(t *testing.T, dir string, limit int64, n int, cp CrashPoint) {
	t.Helper()
	p, err := Open(dir, limit)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("key%06d", (i*7919)%5000)
		if err := p.Ingest(key, []byte(fmt.Sprintf("val%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	p.SetCrashPoint(cp)
	err = p.Close()
	if cp == CrashNone {
		if err != nil {
			t.Fatalf("reference Close: %v", err)
		}
		return
	}
	if !errors.Is(err, ErrCrashed) {
		t.Fatalf("Close err=%v want ErrCrashed", err)
	}
}

// TestCrashRecovery: crash at each of the three stage boundaries, recover,
// and require byte-identical output to the no-crash reference run.
func TestCrashRecovery(t *testing.T) {
	const n = 3000
	const limit = 4096
	root := t.TempDir()
	refDir := filepath.Join(root, "ref")
	runCrashed(t, refDir, limit, n, CrashNone)
	ref, err := os.ReadFile(filepath.Join(refDir, "output.osrt"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		cp   CrashPoint
	}{
		{"after spill", CrashAfterSpill},
		{"mid merge", CrashMidMerge},
		{"before finalize", CrashBeforeFinalize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, "crash-"+tc.name)
			runCrashed(t, dir, limit, n, tc.cp)
			p2, err := Open(dir, limit)
			if err != nil {
				t.Fatal(err)
			}
			if err := p2.Close(); err != nil {
				t.Fatalf("recovery Close: %v", err)
			}
			got, err := os.ReadFile(p2.OutputPath())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, ref) {
				t.Fatal("recovered output differs from no-crash output")
			}
		})
	}
}

// TestConcurrentIngest: many goroutines ingest while background spills run;
// no record is lost or duplicated, the resident bound holds, and Ingest
// after Close returns a decidable error.
func TestConcurrentIngest(t *testing.T) {
	p, err := Open(t.TempDir(), 1<<16)
	if err != nil {
		t.Fatal(err)
	}
	const goroutines = 8
	const per = 5000
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				key := fmt.Sprintf("key%08d", g*per+i)
				if err := p.Ingest(key, []byte("v")); err != nil {
					t.Error(err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if p.Ingested() != goroutines*per {
		t.Fatalf("ingested %d want %d", p.Ingested(), goroutines*per)
	}
	if p.MaxResident() > p.Limit() {
		t.Fatalf("max resident %d exceeds limit %d", p.MaxResident(), p.Limit())
	}
	out := readOutput(t, p.OutputPath())
	if len(out) != goroutines*per {
		t.Fatalf("output %d want %d", len(out), goroutines*per)
	}
	assertSorted(t, out)
	seen := make(map[string]int)
	for _, r := range out {
		seen[r.Key]++
	}
	for k, c := range seen {
		if c != 1 {
			t.Fatalf("key %s appears %d times", k, c)
		}
	}
	if err := p.Ingest("late", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Ingest after Close: err=%v want ErrClosed", err)
	}
}

// TestIngestCloseRace: Ingest racing Close must either be accepted (and
// then appear in the output) or fail with ErrClosed — never panic, never
// silently drop an accepted record.
func TestIngestCloseRace(t *testing.T) {
	p, err := Open(t.TempDir(), 1<<16)
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; ; i++ {
				key := fmt.Sprintf("r%d-%06d", g, i)
				err := p.Ingest(key, []byte("v"))
				if errors.Is(err, ErrClosed) {
					return
				}
				if err != nil {
					t.Error(err)
					return
				}
				accepted.Add(1)
			}
		}(g)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	out := readOutput(t, p.OutputPath())
	if int64(len(out)) != accepted.Load() {
		t.Fatalf("output %d, accepted %d", len(out), accepted.Load())
	}
	assertSorted(t, out)
}
