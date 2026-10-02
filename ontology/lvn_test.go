package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// naiveResult is the naive reference model's answer for one instruction.
type naiveResult struct {
	vn     int
	reused bool
	err    string // "" means accepted
}

// naiveModel re-implements the specification literally and independently.
type naiveModel struct {
	sealed   bool
	nextVN   int
	table    map[string]int
	versions map[int]int
	known    map[int]int
	epoch    int
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		nextVN:   1,
		table:    map[string]int{},
		versions: map[int]int{},
		known:    map[int]int{},
	}
}

func (m *naiveModel) validVN(v int) bool { return v >= 1 && v < m.nextVN }

func (m *naiveModel) step(ins Ins) naiveResult {
	// Rejection precedence: sealed -> operand -> slot.
	if m.sealed {
		return naiveResult{err: ErrSealed.Reason}
	}
	switch ins.Kind {
	case OpAdd, OpMul, OpSub:
		if !m.validVN(ins.A) || !m.validVN(ins.B) {
			return naiveResult{err: ErrBadValue.Reason}
		}
	case OpStore:
		if !m.validVN(ins.V) {
			return naiveResult{err: ErrBadValue.Reason}
		}
	}
	if (ins.Kind == OpLoad || ins.Kind == OpStore) && ins.Slot < 0 {
		return naiveResult{err: ErrBadSlot.Reason}
	}

	switch ins.Kind {
	case OpConst:
		key := fmt.Sprintf("CONST %d", ins.Val)
		if vn, ok := m.table[key]; ok {
			return naiveResult{vn: vn, reused: true}
		}
		vn := m.nextVN
		m.nextVN++
		m.table[key] = vn
		return naiveResult{vn: vn}

	case OpAdd, OpMul, OpSub:
		x, y := ins.A, ins.B
		if (ins.Kind == OpAdd || ins.Kind == OpMul) && x > y {
			x, y = y, x
		}
		key := fmt.Sprintf("%s(%d,%d)", binopName(ins.Kind), x, y)
		if vn, ok := m.table[key]; ok {
			return naiveResult{vn: vn, reused: true}
		}
		vn := m.nextVN
		m.nextVN++
		m.table[key] = vn
		return naiveResult{vn: vn}

	case OpLoad:
		if vn, ok := m.known[ins.Slot]; ok {
			return naiveResult{vn: vn, reused: true}
		}
		ver := m.versions[ins.Slot]
		key := fmt.Sprintf("LOAD(slot=%d,ver=%d,epoch=%d)", ins.Slot, ver, m.epoch)
		if vn, ok := m.table[key]; ok {
			return naiveResult{vn: vn, reused: true}
		}
		vn := m.nextVN
		m.nextVN++
		m.table[key] = vn
		return naiveResult{vn: vn}

	case OpStore:
		if cur, ok := m.known[ins.Slot]; ok && cur == ins.V {
			return naiveResult{reused: true}
		}
		m.versions[ins.Slot]++
		m.known[ins.Slot] = ins.V
		return naiveResult{}

	case OpCall:
		m.epoch++
		m.known = map[int]int{}
		vn := m.nextVN
		m.nextVN++
		return naiveResult{vn: vn}

	default:
		return naiveResult{err: "unknown instruction kind"}
	}
}

func (m *naiveModel) seal() error {
	if m.sealed {
		return ErrSealed
	}
	m.sealed = true
	return nil
}

// runScenario executes instructions through both the Block and the naive
// model, logging input, output and decision basis for every instruction.
func runScenario(t *testing.T, name string, in []Ins) (*Block, *naiveModel) {
	t.Helper()
	b := NewBlock()
	m := newNaiveModel()
	t.Logf("=== scenario %q (%d instructions) ===", name, len(in))
	for i, ins := range in {
		res, err := b.Append(ins)
		want := m.step(ins)

		switch {
		case want.err != "":
			if err == nil {
				t.Fatalf("step %d %s: want rejection %q, got %+v", i, ins, want.err, res)
			}
			var be *BlockError
			if !errors.As(err, &be) || be.Reason != want.err {
				t.Fatalf("step %d %s: want error %q, got %v", i, ins, want.err, err)
			}
			t.Logf("[%2d] %-12s -> REJECTED (%s)", i, ins, want.err)
		default:
			if err != nil {
				t.Fatalf("step %d %s: unexpected error %v", i, ins, err)
			}
			if res.VN != want.vn || res.Reused != want.reused {
				t.Fatalf("step %d %s: got (vn=%d,reused=%v), model wants (vn=%d,reused=%v)",
					i, ins, res.VN, res.Reused, want.vn, want.reused)
			}
			t.Logf("[%2d] %-12s -> vn=%d reused=%-5v | key=%-28s | %s",
				i, ins, res.VN, res.Reused, res.Key, res.Reason)
		}
	}

	snap := b.Query()
	if snap.NextVN != m.nextVN {
		t.Fatalf("nextVN diverged: block=%d model=%d", snap.NextVN, m.nextVN)
	}
	if snap.Epoch != m.epoch {
		t.Fatalf("epoch diverged: block=%d model=%d", snap.Epoch, m.epoch)
	}
	if len(snap.Known) != len(m.known) {
		t.Fatalf("known map diverged: block=%v model=%v", snap.Known, m.known)
	}
	for s, v := range m.known {
		if snap.Known[s] != v {
			t.Fatalf("known[%d] diverged: block=%d model=%d", s, snap.Known[s], v)
		}
	}
	for s, v := range m.versions {
		if snap.Versions[s] != v {
			t.Fatalf("versions[%d] diverged: block=%d model=%d", s, snap.Versions[s], v)
		}
	}
	if len(snap.Table) != len(m.table) {
		t.Fatalf("table size diverged:\nblock=%v\nmodel=%v", snap.Table, m.table)
	}
	for key, vn := range m.table {
		if snap.Table[key] != vn {
			t.Fatalf("table[%q] diverged: block=%d model=%d", key, snap.Table[key], vn)
		}
	}
	return b, m
}

func mustAppend(t *testing.T, b *Block, ins Ins) Result {
	t.Helper()
	res, err := b.Append(ins)
	if err != nil {
		t.Fatalf("%s: %v", ins, err)
	}
	return res
}

func TestCommutativity(t *testing.T) {
	runScenario(t, "add/mul commute, sub does not", []Ins{
		Const(1),  // vn 1
		Const(2),  // vn 2
		Add(1, 2), // vn 3
		Add(2, 1), // reuse 3 (commutative)
		Mul(1, 2), // vn 4
		Mul(2, 1), // reuse 4 (commutative)
		Sub(1, 2), // vn 5
		Sub(2, 1), // vn 6: fresh, non-commutative
		Sub(1, 2), // reuse 5
		Add(1, 2), // reuse 3 again
	})
}

func TestStoreLoadForwarding(t *testing.T) {
	runScenario(t, "store then load forwards", []Ins{
		Const(7), // vn 1
		Store(0, 1),
		Load(0), // forwarded -> vn 1, reused
		Load(0), // still forwarded -> vn 1 (LOAD does not install a known value)
	})
}

func TestRedundantStore(t *testing.T) {
	b, _ := runScenario(t, "redundant store keeps version", []Ins{
		Const(9), // vn 1
		Const(9), // reuse -> 1
		Store(3, 1),
		Store(3, 1), // redundant: reused, no version bump
		Load(3),     // forwarded -> 1
	})
	if v := b.Query().Versions[3]; v != 1 {
		t.Fatalf("slot 3 version = %d, want 1", v)
	}
}

func TestCallBarrier(t *testing.T) {
	runScenario(t, "call clears known values; same slot re-read gets new vn; calls never merge", []Ins{
		Const(5), // vn 1
		Store(2, 1),
		Load(2),   // forward -> 1
		Call(),    // vn 2 (always fresh), epoch 1, known cleared
		Load(2),   // key (slot=2,ver=1,epoch=1) -> vn 3
		Load(2),   // same key -> reuse 3
		Call(),    // vn 4, epoch 2
		Load(2),   // (slot=2,ver=1,epoch=2) -> vn 5
		Add(1, 2), // vn 6: pure keys survive the epoch
		Call(),    // vn 7
		Add(1, 2), // reuse 6: pure keys ignore CALL
	})
}

func TestInvalidOperandsAndSlots(t *testing.T) {
	b, _ := runScenario(t, "rejections and precedence", []Ins{
		Const(1),     // vn 1
		Add(1, 5),    // rejected: vn 5 nonexistent
		Sub(2, 1),    // rejected: vn 2 nonexistent
		Store(0, 9),  // rejected: v 9 nonexistent (operand checked before slot)
		Load(-1),     // rejected: negative slot
		Store(-2, 1), // rejected: negative slot
		Store(1, 1),  // ok
	})
	// Rejected instructions allocate no number: the next allocated VN is 2.
	res, err := b.Append(Const(2))
	if err != nil {
		t.Fatal(err)
	}
	if res.VN != 2 {
		t.Fatalf("vn after rejects = %d, want 2 (rejected instructions occupy no number)", res.VN)
	}

	// After sealing, sealed is reported before operand/slot; re-seal rejects.
	if err := b.Seal(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Append(Add(99, 100)); !errors.Is(err, ErrSealed) {
		t.Fatalf("append after seal: want ErrSealed, got %v", err)
	}
	if err := b.Seal(); !errors.Is(err, ErrSealed) {
		t.Fatalf("re-seal: want ErrSealed, got %v", err)
	}

	// Operand-vs-slot precedence on an unsealed block: operand first.
	b2 := NewBlock()
	if _, err := b2.Append(Store(-1, 42)); !errors.Is(err, ErrBadValue) {
		t.Fatalf("STORE bad v and bad slot: want ErrBadValue (operand checked first), got %v", err)
	}
	if _, err := b2.Append(Load(-1)); !errors.Is(err, ErrBadSlot) {
		t.Fatalf("LOAD negative slot: want ErrBadSlot, got %v", err)
	}
}

func TestConstAcrossCalls(t *testing.T) {
	runScenario(t, "const keys ignore call epoch", []Ins{
		Const(42), // vn 1
		Call(),    // vn 2
		Const(42), // reuse 1
		Const(43), // vn 3
	})
}

func TestReplayDeterminism(t *testing.T) {
	in := []Ins{
		Const(3), Const(4), Add(1, 2), Add(2, 1),
		Store(5, 2), Load(5), Call(), Load(5),
		Mul(1, 3), Mul(3, 1), Sub(2, 1), Sub(1, 2),
		Store(5, 1), Store(5, 1),
	}
	first := replayOnce(in)
	for k := 0; k < 5; k++ {
		if got := replayOnce(in); got != first {
			t.Fatalf("replay %d diverged:\n%s\nvs\n%s", k, got, first)
		}
	}
	t.Logf("deterministic replay log:\n%s", first)
}

func replayOnce(in []Ins) string {
	b := NewBlock()
	var sb strings.Builder
	for _, ins := range in {
		res, err := b.Append(ins)
		if err != nil {
			fmt.Fprintf(&sb, "%s -> ERR %s\n", ins, err)
			continue
		}
		fmt.Fprintf(&sb, "%s -> vn=%d reused=%v key=%s\n", ins, res.VN, res.Reused, res.Key)
	}
	s := b.Query()
	fmt.Fprintf(&sb, "nextVN=%d epoch=%d known=%s versions=%s\n",
		s.NextVN, s.Epoch, sortedKV(s.Known), sortedKV(s.Versions))
	return sb.String()
}

func sortedKV(m map[int]int) string {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d:%d", k, m[k]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

// TestRandomDifferential generates random instruction streams (including
// invalid ones) and requires Block to match the naive model on every step,
// including value-number density and full table/known/version state.
func TestRandomDifferential(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		// Generate the stream once, driving a throwaway model so the
		// nextVN-dependent operand generation is reproducible.
		gen := rand.New(rand.NewSource(seed))
		genModel := newNaiveModel()
		n := 30 + gen.Intn(70)
		stream := make([]Ins, n)
		for i := range stream {
			stream[i] = randomIns(gen, genModel.nextVN)
			genModel.step(stream[i])
		}

		b := NewBlock()
		m := newNaiveModel()
		maxProduced := 0
		for i, ins := range stream {
			res, err := b.Append(ins)
			want := m.step(ins)
			if want.err != "" {
				var be *BlockError
				if err == nil || !errors.As(err, &be) || be.Reason != want.err {
					t.Fatalf("seed=%d step=%d %s: want err %q, got %v", seed, i, ins, want.err, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("seed=%d step=%d %s: unexpected %v", seed, i, ins, err)
			}
			if res.VN != want.vn || res.Reused != want.reused {
				t.Fatalf("seed=%d step=%d %s: got (%d,%v) want (%d,%v)",
					seed, i, ins, res.VN, res.Reused, want.vn, want.reused)
			}
			if want.vn > maxProduced {
				maxProduced = want.vn
			}
		}

		snap := b.Query()
		if snap.NextVN != m.nextVN {
			t.Fatalf("seed=%d nextVN block=%d model=%d", seed, snap.NextVN, m.nextVN)
		}
		// Dense numbering: the largest allocated VN equals nextVN-1.
		if maxProduced != snap.NextVN-1 {
			t.Fatalf("seed=%d max vn=%d but nextVN-1=%d (hole in numbering)",
				seed, maxProduced, snap.NextVN-1)
		}
		if snap.Epoch != m.epoch {
			t.Fatalf("seed=%d epoch block=%d model=%d", seed, snap.Epoch, m.epoch)
		}
		if len(snap.Known) != len(m.known) {
			t.Fatalf("seed=%d known diverged %v vs %v", seed, snap.Known, m.known)
		}
		for s, v := range m.known {
			if snap.Known[s] != v {
				t.Fatalf("seed=%d known[%d] block=%d model=%d", seed, s, snap.Known[s], v)
			}
		}
		for s, v := range m.versions {
			if snap.Versions[s] != v {
				t.Fatalf("seed=%d versions[%d] block=%d model=%d", seed, s, snap.Versions[s], v)
			}
		}
		for key, vn := range m.table {
			if snap.Table[key] != vn {
				t.Fatalf("seed=%d table[%q] block=%d model=%d", seed, key, snap.Table[key], vn)
			}
		}
	}
}

func randomIns(rng *rand.Rand, nextVN int) Ins {
	// Usually reference existing value numbers; occasionally overshoot to
	// exercise rejection. Slots are small and rarely negative.
	pickVN := func() int {
		if nextVN > 1 && rng.Intn(6) != 0 {
			return 1 + rng.Intn(nextVN-1)
		}
		if rng.Intn(5) == 0 {
			return nextVN + rng.Intn(3)
		}
		return 1
	}
	slot := func() int {
		if rng.Intn(10) == 0 {
			return -1 - rng.Intn(3)
		}
		return rng.Intn(4)
	}
	switch rng.Intn(7) {
	case 0:
		return Const(rng.Int63n(5))
	case 1:
		return Load(slot())
	case 2:
		return Add(pickVN(), pickVN())
	case 3:
		return Mul(pickVN(), pickVN())
	case 4:
		return Sub(pickVN(), pickVN())
	case 5:
		return Store(slot(), pickVN())
	default:
		return Call()
	}
}

// TestConcurrent appends, seals and queries from many goroutines, verifying
// safety under -race, exactly one winning Seal, and stable post-seal state.
func TestConcurrent(t *testing.T) {
	b := NewBlock()
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = b.Append(Const(int64(g)))
				_, _ = b.Append(Call())
			}
		}(g)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = b.Query()
		}
	}()

	// Many racers attempt to seal; the mutex serializes them.
	sealResults := make(chan error, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sealResults <- b.Seal()
		}()
	}

	// Wait until sealing has taken effect, then release background loops.
	for {
		if _, err := b.Append(Const(1)); errors.Is(err, ErrSealed) {
			break
		}
	}
	close(stop)
	wg.Wait()
	close(sealResults)

	ok, rej := 0, 0
	for e := range sealResults {
		switch {
		case e == nil:
			ok++
		case errors.Is(e, ErrSealed):
			rej++
		default:
			t.Fatalf("unexpected seal error %v", e)
		}
	}
	if ok != 1 {
		t.Fatalf("successful seals = %d, want exactly 1 (rejected=%d)", ok, rej)
	}

	s := b.Query()
	if !s.Sealed {
		t.Fatal("snapshot reports unsealed after Seal succeeded")
	}
	if s.NextVN < 1 {
		t.Fatalf("impossible nextVN %d", s.NextVN)
	}
	if _, err := b.Append(Const(777)); !errors.Is(err, ErrSealed) {
		t.Fatalf("post-seal append err=%v, want ErrSealed", err)
	}
}
