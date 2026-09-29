package scheduler

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type refTx struct {
	seq     int
	reads   []string
	keys    []string
	writeKV map[string]string
}

func naiveDepths(txs []refTx) map[int]int {
	depths := map[int]int{}
	for i, tx := range txs {
		depth := 1
		for j := 0; j < i; j++ {
			if keyIntersects(txs[j].keys, tx.keys) {
				if depths[txs[j].seq]+1 > depth {
					depth = depths[txs[j].seq] + 1
				}
			}
		}
		depths[tx.seq] = depth
	}
	return depths
}

func naiveReplay(txs []refTx) map[string]string {
	state := map[string]string{}
	for _, tx := range txs {
		for _, key := range tx.keys {
			state[key] = tx.writeKV[key]
		}
	}
	return state
}

func naiveRounds(txs []refTx, maxParallel int) [][]int {
	done := map[int]bool{}
	var rounds [][]int
	pending := append([]refTx(nil), txs...)
	for len(pending) > 0 {
		var round []int
		taken := map[string]bool{}
		var rest []refTx
		for _, tx := range pending {
			if len(round) >= maxParallel {
				rest = append(rest, tx)
				continue
			}
			ready := true
			for _, other := range txs {
				if other.seq >= tx.seq || done[other.seq] {
					continue
				}
				if keyIntersects(other.keys, tx.keys) {
					ready = false
					break
				}
			}
			conflict := false
			if ready {
				for _, key := range tx.keys {
					if taken[key] {
						conflict = true
						break
					}
				}
			}
			if ready && !conflict {
				round = append(round, tx.seq)
				for _, key := range tx.keys {
					taken[key] = true
				}
			} else {
				rest = append(rest, tx)
			}
		}
		for _, seq := range round {
			done[seq] = true
		}
		rounds = append(rounds, round)
		pending = rest
	}
	return rounds
}

func keyIntersects(a, b []string) bool {
	set := map[string]bool{}
	for _, key := range a {
		set[key] = true
	}
	for _, key := range b {
		if set[key] {
			return true
		}
	}
	return false
}

func generate(rng *rand.Rand, n, keySpace int) []Transaction {
	txs := make([]Transaction, n)
	for i := 0; i < n; i++ {
		maxWrites := 4
		if keySpace < maxWrites {
			maxWrites = keySpace
		}
		writeCount := 1 + rng.Intn(maxWrites)
		writes := map[string]string{}
		for len(writes) < writeCount {
			key := fmt.Sprintf("k%d", rng.Intn(keySpace))
			writes[key] = fmt.Sprintf("t%d-%s", i+1, key)
		}
		reads := []string{fmt.Sprintf("k%d", rng.Intn(keySpace))}
		txs[i] = Transaction{Seq: i + 1, Reads: reads, Writes: writes}
	}
	return txs
}

func TestAgainstNaiveReference(t *testing.T) {
	cases := []struct{ n, space int }{
		{1, 1}, {2, 1}, {5, 1}, {5, 2}, {10, 3}, {20, 5}, {50, 8}, {30, 30},
	}
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		tc := cases[rng.Intn(len(cases))]
		raw := generate(rng, tc.n, tc.space)

		var logs bytes.Buffer
		s := New(&logs)
		if err := s.Add(raw); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}

		refs := make([]refTx, len(raw))
		for i, input := range raw {
			keys := make([]string, 0, len(input.Writes))
			for key := range input.Writes {
				keys = append(keys, key)
			}
			refs[i] = refTx{seq: input.Seq, reads: input.Reads, keys: keys, writeKV: input.Writes}
		}

		wantDepths := naiveDepths(refs)
		gotDepths := s.Depths()
		for seq, depth := range wantDepths {
			if gotDepths[seq] != depth {
				t.Fatalf("seed %d: depth[%d]=%d want %d", seed, seq, gotDepths[seq], depth)
			}
		}

		wantState := naiveReplay(refs)
		for _, maxParallel := range []int{1, 2, 3, tc.n, tc.n + 5} {
			rounds, err := s.Schedule(maxParallel)
			if err != nil {
				t.Fatal(err)
			}
			wantRounds := naiveRounds(refs, maxParallel)
			if len(rounds) != len(wantRounds) {
				t.Fatalf("seed %d p=%d: %d rounds want %d", seed, maxParallel, len(rounds), len(wantRounds))
			}
			for i, round := range rounds {
				if len(round.Transaction) != len(wantRounds[i]) {
					t.Fatalf("seed %d p=%d round %d size mismatch", seed, maxParallel, i+1)
				}
				if len(round.Transaction) > maxParallel {
					t.Fatalf("seed %d: round %d exceeds parallelism %d", seed, i+1, maxParallel)
				}
				for j, seq := range round.Transaction {
					if seq != wantRounds[i][j] {
						t.Fatalf("seed %d p=%d round %d: got %v want %v", seed, maxParallel, i+1, round.Transaction, wantRounds[i])
					}
				}
			}

			result, err := s.Replay(maxParallel)
			if err != nil {
				t.Fatal(err)
			}
			if !result.SelfCheckOK {
				t.Fatalf("seed %d p=%d: self-check failed", seed, maxParallel)
			}
			if len(result.State) != len(wantState) {
				t.Fatalf("seed %d: state size mismatch", seed)
			}
			for key, value := range wantState {
				if result.State[key] != value {
					t.Fatalf("seed %d p=%d: state[%s]=%q want %q", seed, maxParallel, key, result.State[key], value)
				}
			}
		}
	}
}

func TestDisjointAndIndependentCases(t *testing.T) {
	s := New(nil)
	err := s.Add([]Transaction{
		tx(1, nil, map[string]string{"a": "1"}),
		tx(2, nil, map[string]string{"b": "2"}),
		tx(3, nil, map[string]string{"c": "3"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	rounds, err := s.Schedule(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 1 || len(rounds[0].Transaction) != 3 {
		t.Fatalf("disjoint txs should share one round, got %+v", rounds)
	}
	for _, depth := range s.Depths() {
		if depth != 1 {
			t.Fatalf("disjoint txs must have depth 1, got %v", s.Depths())
		}
	}

	chain := New(nil)
	if err := chain.Add([]Transaction{
		tx(1, []string{"a"}, map[string]string{"a": "1"}),
		tx(2, []string{"a"}, map[string]string{"a": "2"}),
		tx(3, []string{"a"}, map[string]string{"a": "3"}),
	}); err != nil {
		t.Fatal(err)
	}
	chainRounds, err := chain.Schedule(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(chainRounds) != 3 {
		t.Fatalf("chain needs 3 rounds, got %d", len(chainRounds))
	}
	depths := chain.Depths()
	for seq, want := range map[int]int{1: 1, 2: 2, 3: 3} {
		if depths[seq] != want {
			t.Fatalf("chain depth[%d]=%d want %d", seq, depths[seq], want)
		}
	}
	result, err := chain.Replay(8)
	if err != nil || !result.SelfCheckOK || result.State["a"] != "3" {
		t.Fatalf("chain replay: %+v err=%v", result, err)
	}
}

func TestParallelismOneMatchesSerial(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	raw := generate(rng, 25, 6)
	s := New(nil)
	if err := s.Add(raw); err != nil {
		t.Fatal(err)
	}
	rounds, err := s.Schedule(1)
	if err != nil {
		t.Fatal(err)
	}
	for i, round := range rounds {
		if len(round.Transaction) != 1 || round.Transaction[0] != i+1 {
			t.Fatalf("p=1 must replay in commit order: %+v", rounds)
		}
	}
}

func TestRoundsExecuteConcurrently(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	n := 64
	raw := generate(rng, n, 400)
	s := New(nil)
	if err := s.Add(raw); err != nil {
		t.Fatal(err)
	}

	var active, peak int32
	var wg sync.WaitGroup
	wg.Add(n)
	replayHook = func(*accepted) {
		cur := atomic.AddInt32(&active, 1)
		for {
			observed := atomic.LoadInt32(&peak)
			if cur <= observed || atomic.CompareAndSwapInt32(&peak, observed, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		wg.Done()
	}
	defer func() { replayHook = func(*accepted) {} }()

	start := time.Now()
	result, err := s.Replay(64)
	if err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	elapsed := time.Since(start)
	if !result.SelfCheckOK {
		t.Fatal("self-check failed")
	}
	if peak < 2 {
		t.Fatalf("expected true intra-round concurrency, peak active = %d", peak)
	}
	if elapsed >= time.Duration(n)*20*time.Millisecond {
		t.Fatalf("replay ran serially: %v for %d txs", elapsed, n)
	}
}
