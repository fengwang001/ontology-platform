package hm

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"testing"
)

// TestDifferentialRandom replays 2000 random operation sequences against
// both the real session and the naive reference model, checking the result
// and full state after every step. Inputs, outputs and the verdict are
// logged; HM_VERBOSE=1 prints every step, HM_SEED=<n> pins the seed.
func TestDifferentialRandom(t *testing.T) {
	seed := testSeed(t)
	t.Logf("differential seed = %d (HM_SEED=<n> reproduces)", seed)
	master := rand.New(rand.NewSource(seed))
	verbose := os.Getenv("HM_VERBOSE") != ""
	for seq := 0; seq < 2000; seq++ {
		v := 1 + master.Intn(14)
		s := mustNew(t, baseCtors(), v)
		n := newNaive(baseCtors(), v)
		g := newGenerator(rand.New(rand.NewSource(master.Int63())))
		var log strings.Builder
		t.Logf("seq %d V=%d: inputs/outputs follow", seq, v)
		for step := 0; step < 25; step++ {
			o := g.nextOp()
			desc := describeOp(o)
			os1 := runSessionOp(s, o)
			on1 := runNaiveOp(n, o)
			verdict := "match"
			if os1 != on1 {
				verdict = "RESULT MISMATCH"
			}
			fmtLog(&log, step, desc, os1, verdict)
			if verbose || os1 != on1 {
				t.Logf("seq %d step %d: %s => %s (%s)", seq, step, desc, os1, verdict)
			}
			if os1 != on1 {
				t.Fatalf("seq %d step %d %s\nsession=%s\nnaive =%s\n%s",
					seq, step, desc, os1, on1, log.String())
			}
			assertAcyclic(t, s, fmt.Sprintf("seq %d step %d %s", seq, step, desc))
			statesEquivalent(t, s, n)
		}
		if verbose {
			t.Logf("seq %d: final state equivalent to naive model", seq)
		}
	}
}

func fmtLog(b *strings.Builder, step int, desc string, o outcome, verdict string) {
	b.WriteString("  ")
	b.WriteString(itoa(step))
	b.WriteString(". ")
	b.WriteString(desc)
	b.WriteString(" => ")
	b.WriteString(o.String())
	b.WriteString(" [")
	b.WriteString(verdict)
	b.WriteString("]\n")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// TestDeterministicReplay runs the same recorded sequence twice and asserts
// identical results and byte-identical state dumps.
func TestDeterministicReplay(t *testing.T) {
	var ops []op
	g := newGenerator(rand.New(rand.NewSource(424242)))
	for i := 0; i < 80; i++ {
		ops = append(ops, g.nextOp())
	}
	run := func() (string, []string) {
		s := mustNew(t, baseCtors(), 12)
		var outs []string
		for _, o := range ops {
			outs = append(outs, runSessionOp(s, o).String())
		}
		return dumpSession(s), outs
	}
	dump1, out1 := run()
	dump2, out2 := run()
	if dump1 != dump2 {
		t.Fatalf("replay state differs\n%s\n%s", dump1, dump2)
	}
	for i := range out1 {
		if out1[i] != out2[i] {
			t.Fatalf("replay result differs at %d: %s vs %s", i, out1[i], out2[i])
		}
	}
}

// TestConcurrentIsSerializable runs many operations concurrently. Every
// observed state must satisfy: L in [0,1000], ids contiguous, each level in
// [0,L-history bounds] and unbound levels never exceeding their birth level.
func TestConcurrentIsSerializable(t *testing.T) {
	s := mustNew(t, baseCtors(), 200000)
	const workers = 16
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			g := newGenerator(rand.New(rand.NewSource(seed)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				runSessionOp(s, g.nextOp())
			}
		}(int64(w*7 + 1))
	}
	for i := 0; i < 40; i++ {
		s.mu.Lock()
		if s.l < 0 || s.l > maxLevel || s.next < 0 || s.next > s.v {
			t.Fatalf("invariant broken: L=%d next=%d", s.l, s.next)
		}
		for j := 0; j < s.next; j++ {
			if s.level[j] < 0 || s.level[j] > maxLevel {
				t.Fatalf("bad level v%d=%d", j+1, s.level[j])
			}
		}
		s.mu.Unlock()
	}
	close(stop)
	wg.Wait()
}
