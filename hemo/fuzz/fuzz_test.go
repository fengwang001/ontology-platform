// Package fuzz_test runs thousands of random operation sequences against
// both the production engine (hemo) and the independent naive reference
// model, comparing error codes and full assignment snapshots after every
// step. Each step's input, outputs and decision basis are logged as JSONL.
package fuzz_test

import (
	"encoding/json"
	"flag"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var (
	seqCount = flag.Int("seqs", 1500, "number of random operation sequences")
	opCount  = flag.Int("ops", 40, "operations per sequence")
	logDir   = flag.String("logdir", "testdata/logs", "directory for JSONL logs")
	parallel = flag.Bool("parallel", true, "run sequences concurrently")
)

func TestDifferential1500(t *testing.T) {
	if err := os.MkdirAll(*logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(*logDir, "differential.jsonl")
	f, err := os.Create(logFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)

	var firstMismatch error
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)

	for i := 0; i < *seqCount; i++ {
		i := i
		wg.Add(1)
		run := func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(1000 + i)))
			// One shared JSONL log; writes are serialized by the mutex.
			var local []stepLog
			err := runSeq(i, *opCount, rng, &local)
			mu.Lock()
			for _, row := range local {
				_ = enc.Encode(row)
			}
			if err != nil && firstMismatch == nil {
				firstMismatch = err
			}
			mu.Unlock()
		}
		if *parallel {
			sem <- struct{}{}
			go func() { defer func() { <-sem }(); run() }()
		} else {
			run()
		}
	}
	wg.Wait()
	if firstMismatch != nil {
		t.Fatal(firstMismatch)
	}
	t.Logf("differential: %d sequences x %d ops, log=%s",
		*seqCount, *opCount, logFile)
}
