package lvn_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/lvn"
)

// step appends one instruction, logging input, output and rationale.
func step(t *testing.T, b *lvn.Block, in lvn.Instr) lvn.Result {
	t.Helper()
	res, err := b.Append(in)
	if err != nil {
		t.Fatalf("append %v: unexpected error: %v", in, err)
	}
	t.Logf("in=%-16s out={value:%d reused:%v redundant:%v} reason=%s",
		in, res.Value, res.Reused, res.Redundant, res.Reason)
	return res
}

func TestCommutativeReuse(t *testing.T) {
	b := lvn.NewBlock()
	c1 := step(t, b, lvn.Const(10))
	c2 := step(t, b, lvn.Const(20))
	add1 := step(t, b, lvn.Add(c1.Value, c2.Value))
	add2 := step(t, b, lvn.Add(c2.Value, c1.Value)) // swapped: must reuse
	if !add2.Reused || add2.Value != add1.Value {
		t.Errorf("ADD swapped: got value=%d reused=%v, want value=%d reused=true",
			add2.Value, add2.Reused, add1.Value)
	}
	mul1 := step(t, b, lvn.Mul(c1.Value, c2.Value))
	mul2 := step(t, b, lvn.Mul(c2.Value, c1.Value)) // swapped: must reuse
	if !mul2.Reused || mul2.Value != mul1.Value {
		t.Errorf("MUL swapped: got value=%d reused=%v, want value=%d reused=true",
			mul2.Value, mul2.Reused, mul1.Value)
	}
	sub1 := step(t, b, lvn.Sub(c1.Value, c2.Value))
	sub2 := step(t, b, lvn.Sub(c2.Value, c1.Value)) // swapped: must NOT reuse
	if sub2.Reused || sub2.Value == sub1.Value {
		t.Errorf("SUB swapped: got value=%d reused=%v, want a fresh number",
			sub2.Value, sub2.Reused)
	}
	// Value numbers must be consecutive with no holes.
	want := []int{1, 2, 3, 3, 4, 4, 5, 6}
	got := []int{c1.Value, c2.Value, add1.Value, add2.Value, mul1.Value, mul2.Value, sub1.Value, sub2.Value}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("value numbers = %v, want %v", got, want)
	}
}

func TestStoreLoadForwarding(t *testing.T) {
	b := lvn.NewBlock()
	c := step(t, b, lvn.Const(7))
	st := step(t, b, lvn.Store(0, c.Value))
	if st.Redundant || st.Value != 0 {
		t.Errorf("first STORE: got %+v, want Value=0 Redundant=false", st)
	}
	ld := step(t, b, lvn.Load(0))
	if !ld.Reused || ld.Value != c.Value {
		t.Errorf("LOAD after STORE: got value=%d reused=%v, want forwarded value=%d",
			ld.Value, ld.Reused, c.Value)
	}
}

func TestLoadTableReuse(t *testing.T) {
	b := lvn.NewBlock()
	l1 := step(t, b, lvn.Load(2))
	l2 := step(t, b, lvn.Load(2)) // same slot, version and epoch: must reuse
	if !l2.Reused || l2.Value != l1.Value {
		t.Errorf("second LOAD: got value=%d reused=%v, want reuse of %d",
			l2.Value, l2.Reused, l1.Value)
	}
}

func TestRedundantStore(t *testing.T) {
	b := lvn.NewBlock()
	c := step(t, b, lvn.Const(1))
	step(t, b, lvn.Store(3, c.Value))
	st2 := step(t, b, lvn.Store(3, c.Value))
	if !st2.Redundant {
		t.Errorf("second identical STORE: got redundant=false, want true")
	}
	snap := b.Snapshot()
	if snap.Versions[3] != 1 {
		t.Errorf("redundant STORE bumped version: got ver=%d, want 1", snap.Versions[3])
	}
}

func TestCallBarrier(t *testing.T) {
	b := lvn.NewBlock()
	c := step(t, b, lvn.Const(5))
	step(t, b, lvn.Store(0, c.Value))
	call1 := step(t, b, lvn.Call())
	if call1.Reused {
		t.Errorf("CALL: got reused=true, want a fresh value")
	}
	ld := step(t, b, lvn.Load(0)) // known values cleared by CALL: fresh read
	if ld.Reused || ld.Value == c.Value {
		t.Errorf("LOAD after CALL: got value=%d reused=%v, want a fresh number",
			ld.Value, ld.Reused)
	}
	call2 := step(t, b, lvn.Call())
	if call2.Reused || call2.Value == call1.Value {
		t.Errorf("two CALLs merged: got value=%d reused=%v", call2.Value, call2.Reused)
	}
	// CONST keys ignore the call epoch: the same constant still reuses.
	c2 := step(t, b, lvn.Const(5))
	if !c2.Reused || c2.Value != c.Value {
		t.Errorf("CONST after CALL: got value=%d reused=%v, want reuse of %d",
			c2.Value, c2.Reused, c.Value)
	}
	// A LOAD result does not set the known value: storing it is not redundant.
	st := step(t, b, lvn.Store(0, ld.Value))
	if st.Redundant {
		t.Errorf("STORE of LOAD result marked redundant; LOAD must not set known value")
	}
}

func TestRejections(t *testing.T) {
	b := lvn.NewBlock()
	c := step(t, b, lvn.Const(1))

	cases := []struct {
		name string
		in   lvn.Instr
		want error
	}{
		{"bad operand ADD", lvn.Add(c.Value, 99), lvn.ErrOperand},
		{"bad operand STORE", lvn.Store(0, 99), lvn.ErrOperand},
		{"negative slot LOAD", lvn.Load(-1), lvn.ErrSlot},
		{"negative slot STORE", lvn.Store(-2, c.Value), lvn.ErrSlot},
		{"operand checked before slot", lvn.Store(-2, 99), lvn.ErrOperand},
	}
	before := b.Snapshot()
	for _, tc := range cases {
		_, err := b.Append(tc.in)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got err=%v, want %v", tc.name, err, tc.want)
		}
		t.Logf("in=%v rejected: %v", tc.in, err)
	}
	after := b.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Errorf("rejected appends mutated state: before=%+v after=%+v", before, after)
	}

	if err := b.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := b.Seal(); !errors.Is(err, lvn.ErrReseal) {
		t.Errorf("re-seal: got err=%v, want ErrReseal", err)
	}
	// Sealed beats operand and slot errors.
	sealedCases := []lvn.Instr{lvn.Const(2), lvn.Add(1, 99), lvn.Load(-1)}
	for _, in := range sealedCases {
		if _, err := b.Append(in); !errors.Is(err, lvn.ErrSealed) {
			t.Errorf("after seal %v: got err=%v, want ErrSealed", in, err)
		}
	}
}

// naive is a straightforward simulation written directly from the spec:
// a linear-scan entry list with canonical string keys, sharing no code
// with the implementation under test.
type naive struct {
	next     int
	entries  []naiveEntry
	versions map[int64]uint64
	known    map[int64]int
	epoch    uint64
}

type naiveEntry struct {
	key string
	val int
}

type naiveOut struct {
	value     int
	reused    bool
	redundant bool
	err       error
}

func newNaive() *naive {
	return &naive{next: 1, versions: map[int64]uint64{}, known: map[int64]int{}}
}

func (n *naive) lookup(key string) naiveOut {
	for _, e := range n.entries {
		if e.key == key {
			return naiveOut{value: e.val, reused: true}
		}
	}
	v := n.next
	n.next++
	n.entries = append(n.entries, naiveEntry{key, v})
	return naiveOut{value: v}
}

func (n *naive) append(in lvn.Instr) naiveOut {
	op, args := in.Decompose()
	valid := func(v int64) bool { return v >= 1 && v < int64(n.next) }
	switch op {
	case lvn.OpConst:
		return n.lookup(fmt.Sprintf("CONST|%d", args[0]))
	case lvn.OpAdd, lvn.OpMul:
		a, bb := args[0], args[1]
		if !valid(a) || !valid(bb) {
			return naiveOut{err: lvn.ErrOperand}
		}
		if a > bb {
			a, bb = bb, a
		}
		return n.lookup(fmt.Sprintf("%s|%d|%d", op, a, bb))
	case lvn.OpSub:
		a, bb := args[0], args[1]
		if !valid(a) || !valid(bb) {
			return naiveOut{err: lvn.ErrOperand}
		}
		return n.lookup(fmt.Sprintf("SUB|%d|%d", a, bb))
	case lvn.OpLoad:
		s := args[0]
		if s < 0 {
			return naiveOut{err: lvn.ErrSlot}
		}
		if kv, has := n.known[s]; has {
			return naiveOut{value: kv, reused: true}
		}
		return n.lookup(fmt.Sprintf("LOAD|%d|%d|%d", s, n.versions[s], n.epoch))
	case lvn.OpStore:
		s, v := args[0], args[1]
		if !valid(v) {
			return naiveOut{err: lvn.ErrOperand}
		}
		if s < 0 {
			return naiveOut{err: lvn.ErrSlot}
		}
		if kv, has := n.known[s]; has && kv == int(v) {
			return naiveOut{redundant: true}
		}
		n.versions[s]++
		n.known[s] = int(v)
		return naiveOut{}
	case lvn.OpCall:
		n.epoch++
		n.known = map[int64]int{}
		v := n.next
		n.next++
		return naiveOut{value: v}
	}
	return naiveOut{err: fmt.Errorf("unknown op %d", int(op))}
}

// genInstr builds a mostly-valid random instruction. live is the next
// value number to be assigned, so valid operands are in [1, live).
func genInstr(rng *rand.Rand, live int) lvn.Instr {
	switch r := rng.Intn(100); {
	case r < 4: // operand referencing a nonexistent value number
		return lvn.Add(live+rng.Intn(3), 1)
	case r < 7: // negative slot
		return lvn.Load(-1 - int64(rng.Intn(3)))
	}
	constOf := func() lvn.Instr { return lvn.Const(int64(rng.Intn(6))) }
	operand := func() int { return 1 + rng.Intn(live-1) }
	switch rng.Intn(8) {
	case 0:
		return constOf()
	case 1:
		return lvn.Load(int64(rng.Intn(4)))
	case 2, 3:
		if live < 2 {
			return constOf()
		}
		return lvn.Add(operand(), operand())
	case 4:
		if live < 2 {
			return constOf()
		}
		return lvn.Mul(operand(), operand())
	case 5:
		if live < 2 {
			return constOf()
		}
		return lvn.Sub(operand(), operand())
	case 6:
		if live < 2 {
			return constOf()
		}
		return lvn.Store(int64(rng.Intn(4)), operand())
	default:
		return lvn.Call()
	}
}

func sameErr(got, want error) bool {
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}

func TestAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for trial := 0; trial < 200; trial++ {
		b := lvn.NewBlock()
		n := newNaive()
		live := 1
		steps := 1 + rng.Intn(60)
		for i := 0; i < steps; i++ {
			in := genInstr(rng, live)
			res, err := b.Append(in)
			want := n.append(in)
			if trial == 0 {
				t.Logf("step %d in=%-16s out={value:%d reused:%v redundant:%v err:%v}",
					i, in, res.Value, res.Reused, res.Redundant, err)
			}
			if !sameErr(err, want.err) {
				t.Fatalf("trial %d step %d in=%v: got err=%v, want %v", trial, i, in, err, want.err)
			}
			if err != nil {
				continue
			}
			if res.Value != want.value || res.Reused != want.reused || res.Redundant != want.redundant {
				t.Fatalf("trial %d step %d in=%v: got {value:%d reused:%v redundant:%v}, want %+v",
					trial, i, in, res.Value, res.Reused, res.Redundant, want)
			}
			if res.Value > 0 && !res.Reused {
				live++
			}
		}
		snap := b.Snapshot()
		if snap.Next != n.next || snap.Epoch != n.epoch ||
			!reflect.DeepEqual(snap.Versions, n.versions) || !reflect.DeepEqual(snap.Known, n.known) {
			t.Fatalf("trial %d final state mismatch: snap=%+v naive=%+v", trial, snap, n)
		}
	}
}

func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	var seq []lvn.Instr
	live := 1
	for i := 0; i < 100; i++ {
		in := genInstr(rng, live)
		seq = append(seq, in)
		// Track live conservatively: only CONST always assigns here.
		if op, _ := in.Decompose(); op == lvn.OpConst || op == lvn.OpCall {
			live++
		}
	}
	run := func() []lvn.Result {
		b := lvn.NewBlock()
		var out []lvn.Result
		for _, in := range seq {
			res, _ := b.Append(in)
			out = append(out, res)
		}
		return out
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay diverged:\nfirst=%v\nsecond=%v", first, second)
	}
	t.Logf("replayed %d instructions twice with identical results", len(seq))
}

func TestConcurrentDistinctValues(t *testing.T) {
	const goroutines = 64
	b := lvn.NewBlock()
	results := make([]lvn.Result, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := b.Append(lvn.Const(int64(1000 + i)))
			if err != nil {
				t.Errorf("append: %v", err)
			}
			results[i] = res
		}(i)
	}
	wg.Wait()
	seen := map[int]bool{}
	for _, r := range results {
		if r.Reused {
			t.Errorf("distinct CONST reused an existing number: %+v", r)
		}
		if seen[r.Value] {
			t.Errorf("duplicate value number %d", r.Value)
		}
		seen[r.Value] = true
	}
	for v := 1; v <= goroutines; v++ {
		if !seen[v] {
			t.Errorf("value number %d missing: numbering has a hole", v)
		}
	}
}

func TestConcurrentMixed(t *testing.T) {
	b := lvn.NewBlock()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var assigned []int
	worker := func(seed int64) {
		defer wg.Done()
		rng := rand.New(rand.NewSource(seed))
		for i := 0; i < 200; i++ {
			var in lvn.Instr
			switch rng.Intn(5) {
			case 0:
				in = lvn.Const(int64(rng.Intn(4)))
			case 1:
				in = lvn.Load(int64(rng.Intn(3)))
			case 2:
				in = lvn.Store(int64(rng.Intn(3)), 1) // may be rejected early on
			case 3:
				in = lvn.Call()
			default:
				in = lvn.Add(1, 1) // may be rejected early on
			}
			res, err := b.Append(in)
			if err == nil && !res.Reused && res.Value > 0 {
				mu.Lock()
				assigned = append(assigned, res.Value)
				mu.Unlock()
			}
			if i%50 == 0 {
				_ = b.Snapshot()
			}
		}
	}
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go worker(int64(w))
	}
	wg.Wait()
	if err := b.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// Freshly assigned numbers must be exactly 1..Next-1 with no holes.
	sort.Ints(assigned)
	snap := b.Snapshot()
	if len(assigned) != snap.Next-1 {
		t.Fatalf("assigned %d fresh numbers, but Next=%d", len(assigned), snap.Next)
	}
	for i, v := range assigned {
		if v != i+1 {
			t.Fatalf("value numbers not contiguous: %v", assigned)
		}
	}
	if _, err := b.Append(lvn.Const(1)); !errors.Is(err, lvn.ErrSealed) {
		t.Errorf("append after seal: got err=%v, want ErrSealed", err)
	}
	t.Logf("8 workers appended concurrently; %d fresh numbers, all contiguous", len(assigned))
}
