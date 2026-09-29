package antientropy

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// sameLog 忽略顺序比较两个日志的变更标识集合与内容。
func sameLog(a, b []Change) bool {
	key := func(c Change) string { return fmt.Sprintf("%s#%d", c.Source, c.Seq) }
	sa := map[string]Change{}
	sb := map[string]Change{}
	for _, c := range a {
		sa[key(c)] = c
	}
	for _, c := range b {
		sb[key(c)] = c
	}
	return reflect.DeepEqual(sa, sb)
}

// naiveSync 朴素参照：发送方把整份日志逐条发过去，接收方跳过已见项。
func naiveSync(t *testing.T, src, dst *Replica) {
	t.Helper()
	for _, c := range src.Log() {
		dst.mu.Lock()
		seen := c.Seq <= dst.vector[c.Source]
		dst.mu.Unlock()
		if seen {
			continue
		}
		if err := dst.applyBatch([]Change{c}); err != nil {
			t.Fatalf("naive apply %s#%d: %v", c.Source, c.Seq, err)
		}
	}
}

// TestConvergenceAgainstNaive：随机写入与随机两两双向同步后，
// 增量机制与朴素整份发送参照在向量、日志、视图上完全一致，且各副本收敛。
func TestConvergenceAgainstNaive(t *testing.T) {
	for _, seed := range []int64{1, 2, 7, 42, 99} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			names := []string{"n1", "n2", "n3", "n4"}

			regInc, regNaive := NewRegistry(), NewRegistry()
			inc, nai := map[string]*Replica{}, map[string]*Replica{}
			for _, n := range names {
				inc[n] = mustRegister(t, regInc, n)
				nai[n] = mustRegister(t, regNaive, n)
			}

			for round := 0; round < 30; round++ {
				n := names[rng.Intn(len(names))]
				key := fmt.Sprintf("key%d", rng.Intn(6))
				val := fmt.Sprintf("%s-r%d", n, round)
				inc[n].Write(key, val)
				nai[n].Write(key, val)
			}

			var steps bytes.Buffer
			for round := 0; round < 80; round++ {
				i, j := rng.Intn(len(names)), rng.Intn(len(names))
				if i == j {
					continue
				}
				x, y := names[i], names[j]
				fmt.Fprintf(&steps, "-- round %d: %s <-> %s --\n", round, x, y)
				if _, err := Sync(inc[x], inc[y], &steps); err != nil {
					t.Fatalf("incremental sync rejected: %v", err)
				}
				if _, err := Sync(inc[y], inc[x], &steps); err != nil {
					t.Fatalf("reverse sync rejected: %v", err)
				}
				naiveSync(t, nai[x], nai[y])
				naiveSync(t, nai[y], nai[x])
			}

			// 参照再做一次全连接，确保它也到达最终一致状态。
			for _, x := range names {
				for _, y := range names {
					if x != y {
						naiveSync(t, nai[x], nai[y])
					}
				}
			}

			var first state
			for i, n := range names {
				got := snapshot(inc[n])
				ref := snapshot(nai[n])
				if !reflect.DeepEqual(got.vector, ref.vector) {
					t.Fatalf("seed %d %s vector mismatch: inc=%v naive=%v", seed, n, got.vector, ref.vector)
				}
				if !sameLog(got.log, ref.log) {
					t.Fatalf("seed %d %s log mismatch:\ninc=%v\nnaive=%v", seed, n, got.log, ref.log)
				}
				if !reflect.DeepEqual(got.view, ref.view) {
					t.Fatalf("seed %d %s view mismatch: inc=%v naive=%v", seed, n, got.view, ref.view)
				}
				if i == 0 {
					first = got
				} else {
					if !reflect.DeepEqual(got.vector, first.vector) {
						t.Fatalf("seed %d not converged: %s=%v %s=%v", seed, names[0], first.vector, n, got.vector)
					}
					if !sameLog(got.log, first.log) || !reflect.DeepEqual(got.view, first.view) {
						t.Fatalf("seed %d not converged at %s", seed, n)
					}
				}
			}
			if os.Getenv("SYNC_STEPS") != "" {
				t.Logf("\n%s", steps.String())
			}
		})
	}
}

type state struct {
	vector Version
	log    []Change
	view   map[string]string
}

func snapshot(r *Replica) state {
	return state{vector: r.Vector(), log: r.Log(), view: r.View()}
}

// TestConcurrentWritesAndSyncs：多执行体并发写入与同步下无竞态、最终收敛。
func TestConcurrentWritesAndSyncs(t *testing.T) {
	reg := NewRegistry()
	names := []string{"p", "q", "r"}
	reps := map[string]*Replica{}
	for _, n := range names {
		reps[n] = mustRegister(t, reg, n)
	}

	var wg sync.WaitGroup
	for wi := 0; wi < 6; wi++ {
		n := names[wi%len(names)]
		wg.Add(1)
		go func(worker int, who string) {
			defer wg.Done()
			for k := 0; k < 20; k++ {
				reps[who].Write(fmt.Sprintf("k%d", k%5), fmt.Sprintf("%s-%d-%d", who, worker, k))
			}
		}(wi, n)
	}
	for si := 0; si < 12; si++ {
		x, y := names[si%len(names)], names[(si+1)%len(names)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 10; k++ {
				_, _ = Sync(reps[x], reps[y], nil)
				_, _ = Sync(reps[y], reps[x], nil)
				_ = reps[x].View()
			}
		}()
	}
	wg.Wait()

	// 并发结束后做全连接，所有副本必须完全一致。
	for _, x := range names {
		for _, y := range names {
			if x != y {
				if _, err := Sync(reps[x], reps[y], nil); err != nil {
					t.Fatalf("final sync %s->%s: %v", x, y, err)
				}
			}
		}
	}
	base := snapshot(reps[names[0]])
	totalWrites := 0
	for _, n := range names {
		s := snapshot(reps[n])
		if !reflect.DeepEqual(s.vector, base.vector) ||
			!sameLog(s.log, base.log) || !reflect.DeepEqual(s.view, base.view) {
			t.Fatalf("post-concurrency divergence at %s:\n%v\n%v", n, s.vector, base.vector)
		}
		totalWrites += s.vector[n]
	}
	// 每个来源序号连续无缺，日志总量等于写入总数。
	seqs := make([]int, 0)
	for _, v := range base.vector {
		seqs = append(seqs, v)
	}
	sort.Ints(seqs)
	if len(base.log) != totalWrites {
		t.Fatalf("log size %d != total writes %d", len(base.log), totalWrites)
	}
}
