package ontology

import (
	"fmt"
	"sync"
	"testing"
)

const (
	concWorkers   = 8
	concGroups    = 50
	concRowsPerWk = 2000
	concN         = 7
)

// makeConcRow 生成确定性的行：worker 与序号决定分组、分数与 tie。
func makeConcRow(worker, seq int) map[string]any {
	return map[string]any{
		"g": fmt.Sprintf("g%02d", (worker*concRowsPerWk+seq)%concGroups),
		"s": float64((worker*7919 + seq*104729) % 100003),
		"t": fmt.Sprintf("w%02d", worker),
	}
}

func TestConcurrentAddMatchesSerial(t *testing.T) {
	// 串行基准。
	serial, err := New(testConfig(concN))
	if err != nil {
		t.Fatal(err)
	}
	for w := 0; w < concWorkers; w++ {
		for i := 0; i < concRowsPerWk; i++ {
			serial.Add(makeConcRow(w, i))
		}
	}
	want := serial.Snapshot()

	// 并发喂入同一批行。
	par, err := New(testConfig(concN))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < concWorkers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < concRowsPerWk; i++ {
				par.Add(makeConcRow(worker, i))
			}
		}(w)
	}
	wg.Wait()

	if got := par.Processed(); got != int64(concWorkers*concRowsPerWk) {
		t.Fatalf("lost or double-counted rows: processed=%d want %d",
			got, concWorkers*concRowsPerWk)
	}
	if !snapshotEqual(want, par.Snapshot()) {
		t.Fatal("concurrent result differs from serial result")
	}
}

func TestConcurrentSnapshotNeverSeesOverfullGroup(t *testing.T) {
	s, err := New(testConfig(concN))
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 观察者：并发 Snapshot/Stats，校验不变量。
	for k := 0; k < 2; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				held, groups := s.Stats()
				if held > groups*concN {
					t.Errorf("held %d > groups %d × N %d", held, groups, concN)
					return
				}
				for _, gs := range s.Snapshot() {
					if len(gs.Rows) > concN {
						t.Errorf("group %s holds %d rows > N", gs.Key, len(gs.Rows))
						return
					}
				}
			}
		}()
	}

	// 写者。
	writers := make(chan struct{})
	go func() {
		var wwg sync.WaitGroup
		for w := 0; w < concWorkers; w++ {
			wwg.Add(1)
			go func(worker int) {
				defer wwg.Done()
				for i := 0; i < concRowsPerWk; i++ {
					s.Add(makeConcRow(worker, i))
				}
			}(w)
		}
		wwg.Wait()
		close(writers)
	}()
	<-writers
	close(stop)
	wg.Wait()
}
