package hpack

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func mustEncoder(t *testing.T, c0, l0 int, sensitive ...string) *Encoder {
	t.Helper()
	e, err := NewEncoder(c0, l0, sensitive)
	if err != nil {
		t.Fatalf("NewEncoder(%d,%d): %v", c0, l0, err)
	}
	return e
}

func mustDecoder(t *testing.T, c0, ld int) *Decoder {
	t.Helper()
	d, err := NewDecoder(c0, ld)
	if err != nil {
		t.Fatalf("NewDecoder(%d,%d): %v", c0, ld, err)
	}
	return d
}

func mustEncode(t *testing.T, e *Encoder, headers []Entry) Block {
	t.Helper()
	b, err := e.Encode(headers)
	if err != nil {
		t.Fatalf("Encode(%v): %v", headers, err)
	}
	return b
}

func mustDecode(t *testing.T, d *Decoder, b Block) []Entry {
	t.Helper()
	out, err := d.Decode(b)
	if err != nil {
		t.Fatalf("Decode(%+v): %v", b, err)
	}
	return out
}

func checkTables(t *testing.T, e *Encoder, d *Decoder) {
	t.Helper()
	et, dt := e.Table(), d.Table()
	if !reflect.DeepEqual(et, dt) {
		t.Fatalf("table mismatch:\nencoder=%v\ndecoder=%v", et, dt)
	}
	if e.Capacity() != d.Capacity() {
		t.Fatalf("capacity mismatch: encoder=%d decoder=%d", e.Capacity(), d.Capacity())
	}
	if e.Used() != d.Used() {
		t.Fatalf("used mismatch: encoder=%d decoder=%d", e.Used(), d.Used())
	}
}

// C=60, empty table: Encode [(k,v)] emits LiteralIndexed(0,"k","v") and
// inserts a 34-byte entry; the follow-up Encode [(k,w)] matches the name
// at dynamic index 5, and inserting (k,w) evicts (k,v) because 34+34 > 60.
func TestInsertAndEviction(t *testing.T) {
	enc := mustEncoder(t, 60, 60)
	dec := mustDecoder(t, 60, 60)

	b1 := mustEncode(t, enc, []Entry{{"k", "v"}})
	want1 := Block{Instrs: []Instr{{Kind: KindLiteralIndexed, NameIdx: 0, Name: "k", Value: "v"}}}
	if !reflect.DeepEqual(b1, want1) {
		t.Fatalf("block1 = %+v, want %+v", b1, want1)
	}
	if got := enc.Table(); !reflect.DeepEqual(got, []Entry{{"k", "v"}}) {
		t.Fatalf("table = %v, want [(k v)]", got)
	}
	if enc.Used() != 34 {
		t.Fatalf("used = %d, want 34", enc.Used())
	}
	out1 := mustDecode(t, dec, b1)
	if !reflect.DeepEqual(out1, []Entry{{"k", "v"}}) {
		t.Fatalf("decoded = %v", out1)
	}
	checkTables(t, enc, dec)

	b2 := mustEncode(t, enc, []Entry{{"k", "w"}})
	want2 := Block{Instrs: []Instr{{Kind: KindLiteralIndexed, NameIdx: 5, Name: "", Value: "w"}}}
	if !reflect.DeepEqual(b2, want2) {
		t.Fatalf("block2 = %+v, want %+v", b2, want2)
	}
	if got := enc.Table(); !reflect.DeepEqual(got, []Entry{{"k", "w"}}) {
		t.Fatalf("table = %v, want [(k w)] (evicted (k v))", got)
	}
	// The decoder resolves the name at index 5 before its own eviction.
	out2 := mustDecode(t, dec, b2)
	if !reflect.DeepEqual(out2, []Entry{{"k", "w"}}) {
		t.Fatalf("decoded = %v", out2)
	}
	checkTables(t, enc, dec)
	t.Logf("b1=%+v b2=%+v: 34+34=68>60 evicts oldest; name resolved at idx5 pre-insert", b1, b2)
}

// C=40: an entry of size 8+9+32=49 exceeds the capacity, so the table is
// cleared and the entry is not inserted (not an error).
func TestOversizedEntryClearsTable(t *testing.T) {
	enc := mustEncoder(t, 40, 40)
	dec := mustDecoder(t, 40, 40)

	b1 := mustEncode(t, enc, []Entry{{"a", "b"}}) // size 34, fits
	mustDecode(t, dec, b1)
	if enc.Used() != 34 {
		t.Fatalf("used = %d, want 34", enc.Used())
	}

	b2 := mustEncode(t, enc, []Entry{{"abcdefgh", "ijklmnopq"}}) // size 49 > 40
	want := []Instr{{Kind: KindLiteralIndexed, NameIdx: 0, Name: "abcdefgh", Value: "ijklmnopq"}}
	if !reflect.DeepEqual(b2.Instrs, want) {
		t.Fatalf("instrs = %+v, want %+v", b2.Instrs, want)
	}
	if got := enc.Table(); len(got) != 0 {
		t.Fatalf("table = %v, want empty (cleared, oversized entry not inserted)", got)
	}
	out := mustDecode(t, dec, b2)
	if !reflect.DeepEqual(out, []Entry{{"abcdefgh", "ijklmnopq"}}) {
		t.Fatalf("decoded = %v", out)
	}
	checkTables(t, enc, dec)
	t.Logf("entry size 8+9+32=49 > C=40: table cleared, entry not inserted")
}

// A full static match wins even when the same pair sits in the dynamic
// table.
func TestStaticFullMatchBeatsDynamic(t *testing.T) {
	enc := mustEncoder(t, 100, 100)
	enc.table.insert(":method", "GET", enc.cap) // inject pair into dynamic table
	b := mustEncode(t, enc, []Entry{{":method", "GET"}})
	want := []Instr{{Kind: KindIndexed, Index: 1}}
	if !reflect.DeepEqual(b.Instrs, want) {
		t.Fatalf("instrs = %+v, want %+v (static index 1 beats dynamic 5)", b.Instrs, want)
	}
}

// Name-only matches prefer the smallest static index: (:method, PUT) takes
// nameIdx 1, not 2, and static beats a dynamic name match.
func TestNameMatchStaticSmallest(t *testing.T) {
	enc := mustEncoder(t, 200, 200)
	b1 := mustEncode(t, enc, []Entry{{":method", "PUT"}})
	want1 := []Instr{{Kind: KindLiteralIndexed, NameIdx: 1, Name: "", Value: "PUT"}}
	if !reflect.DeepEqual(b1.Instrs, want1) {
		t.Fatalf("instrs = %+v, want %+v", b1.Instrs, want1)
	}
	// (:method, PUT) is now dynamic index 5; static :method (1) still wins.
	b2 := mustEncode(t, enc, []Entry{{":method", "PATCH"}})
	want2 := []Instr{{Kind: KindLiteralIndexed, NameIdx: 1, Name: "", Value: "PATCH"}}
	if !reflect.DeepEqual(b2.Instrs, want2) {
		t.Fatalf("instrs = %+v, want %+v", b2.Instrs, want2)
	}
}

// Dynamic full match and newest-dynamic-name match.
func TestDynamicMatches(t *testing.T) {
	enc := mustEncoder(t, 1000, 1000)
	mustEncode(t, enc, []Entry{{"k", "v"}, {"k", "w"}, {"j", "z"}})
	// table newest-first: (j,z)=5, (k,w)=6, (k,v)=7
	b := mustEncode(t, enc, []Entry{{"k", "v"}, {"k", "x"}})
	want := []Instr{
		{Kind: KindIndexed, Index: 7},                      // full match (k,v)
		{Kind: KindLiteralIndexed, NameIdx: 6, Value: "x"}, // newest name match (k,w)
	}
	if !reflect.DeepEqual(b.Instrs, want) {
		t.Fatalf("instrs = %+v, want %+v", b.Instrs, want)
	}
}

// Sensitive names emit LiteralNever, are never inserted, but still take a
// name index when one exists.
func TestSensitiveNames(t *testing.T) {
	enc := mustEncoder(t, 100, 100, "authorization", "k")
	dec := mustDecoder(t, 100, 100)

	b1 := mustEncode(t, enc, []Entry{{"authorization", "s3cr3t"}})
	want1 := []Instr{{Kind: KindLiteralNever, NameIdx: 0, Name: "authorization", Value: "s3cr3t"}}
	if !reflect.DeepEqual(b1.Instrs, want1) {
		t.Fatalf("instrs = %+v, want %+v", b1.Instrs, want1)
	}
	if len(enc.Table()) != 0 {
		t.Fatalf("sensitive header must not be inserted, table = %v", enc.Table())
	}
	out1 := mustDecode(t, dec, b1)
	if !reflect.DeepEqual(out1, []Entry{{"authorization", "s3cr3t"}}) {
		t.Fatalf("decoded = %v", out1)
	}

	// Insert (k,v) via a non-sensitive path is impossible (k is sensitive),
	// so inject it directly and confirm the name index is still taken.
	enc.table.insert("k", "v", enc.cap)
	dec.table.insert("k", "v", dec.cap)
	b2 := mustEncode(t, enc, []Entry{{"k", "w"}})
	want2 := []Instr{{Kind: KindLiteralNever, NameIdx: 5, Name: "", Value: "w"}}
	if !reflect.DeepEqual(b2.Instrs, want2) {
		t.Fatalf("instrs = %+v, want %+v", b2.Instrs, want2)
	}
	if got := enc.Table(); !reflect.DeepEqual(got, []Entry{{"k", "v"}}) {
		t.Fatalf("table changed by sensitive header: %v", got)
	}
	out2 := mustDecode(t, dec, b2)
	if !reflect.DeepEqual(out2, []Entry{{"k", "w"}}) {
		t.Fatalf("decoded = %v", out2)
	}
	checkTables(t, enc, dec)
}

// updates generation: lower-then-restore yields [min, current]; a pure
// lower yields [min]; a pure raise yields [current].
func TestUpdatesNegotiation(t *testing.T) {
	t.Run("down then up", func(t *testing.T) {
		enc := mustEncoder(t, 60, 100)
		if err := enc.Resize(10); err != nil {
			t.Fatal(err)
		}
		if err := enc.Resize(60); err != nil {
			t.Fatal(err)
		}
		b := mustEncode(t, enc, nil)
		if !reflect.DeepEqual(b.Updates, []int{10, 60}) {
			t.Fatalf("updates = %v, want [10 60]", b.Updates)
		}
		b = mustEncode(t, enc, nil)
		if len(b.Updates) != 0 {
			t.Fatalf("updates after base reset = %v, want empty", b.Updates)
		}
	})
	t.Run("down only", func(t *testing.T) {
		enc := mustEncoder(t, 60, 100)
		if err := enc.Resize(10); err != nil {
			t.Fatal(err)
		}
		b := mustEncode(t, enc, nil)
		if !reflect.DeepEqual(b.Updates, []int{10}) {
			t.Fatalf("updates = %v, want [10]", b.Updates)
		}
	})
	t.Run("up only", func(t *testing.T) {
		enc := mustEncoder(t, 60, 100)
		if err := enc.Resize(80); err != nil {
			t.Fatal(err)
		}
		b := mustEncode(t, enc, nil)
		if !reflect.DeepEqual(b.Updates, []int{80}) {
			t.Fatalf("updates = %v, want [80]", b.Updates)
		}
	})
	t.Run("setlimit lowers capacity", func(t *testing.T) {
		enc := mustEncoder(t, 60, 100)
		if err := enc.SetLimit(20); err != nil {
			t.Fatal(err)
		}
		if enc.Capacity() != 20 {
			t.Fatalf("capacity = %d, want 20", enc.Capacity())
		}
		b := mustEncode(t, enc, nil)
		if !reflect.DeepEqual(b.Updates, []int{20}) {
			t.Fatalf("updates = %v, want [20]", b.Updates)
		}
	})
	t.Run("setlimit raise does not grow capacity", func(t *testing.T) {
		enc := mustEncoder(t, 60, 100)
		if err := enc.SetLimit(90); err != nil {
			t.Fatal(err)
		}
		if enc.Capacity() != 60 {
			t.Fatalf("capacity = %d, want 60", enc.Capacity())
		}
		b := mustEncode(t, enc, nil)
		if len(b.Updates) != 0 {
			t.Fatalf("updates = %v, want empty", b.Updates)
		}
	})
}

// A failure on the third instruction rolls back the inserts of the first
// two, restores the capacity, and yields no headers.
func TestDecodeRollback(t *testing.T) {
	dec := mustDecoder(t, 100, 100)
	mustDecode(t, dec, Block{Instrs: []Instr{
		{Kind: KindLiteralIndexed, NameIdx: 0, Name: "a", Value: "1"},
	}})
	before := dec.Table()
	beforeCap := dec.Capacity()

	bad := Block{
		Updates: []int{50},
		Instrs: []Instr{
			{Kind: KindLiteralIndexed, NameIdx: 0, Name: "x", Value: "1"},
			{Kind: KindLiteralIndexed, NameIdx: 0, Name: "y", Value: "2"},
			{Kind: KindIndexed, Index: 99}, // out of range
		},
	}
	out, err := dec.Decode(bad)
	if !errors.Is(err, ErrIndex) {
		t.Fatalf("err = %v, want ErrIndex", err)
	}
	if out != nil {
		t.Fatalf("out = %v, want nil on error", out)
	}
	if got := dec.Table(); !reflect.DeepEqual(got, before) {
		t.Fatalf("table = %v, want rolled back to %v", got, before)
	}
	if dec.Capacity() != beforeCap {
		t.Fatalf("capacity = %d, want rolled back to %d", dec.Capacity(), beforeCap)
	}
	t.Logf("block %+v failed at instr 3; inserts of x,y and update 50 rolled back", bad)
}

// ErrSyntax is decided before ErrIndex on the same instruction.
func TestSyntaxBeforeIndex(t *testing.T) {
	dec := mustDecoder(t, 100, 100)
	// nameIdx out of range AND carries a literal name: syntax wins.
	_, err := dec.Decode(Block{Instrs: []Instr{
		{Kind: KindLiteralIndexed, NameIdx: 99, Name: "k", Value: "v"},
	}})
	if !errors.Is(err, ErrSyntax) {
		t.Fatalf("err = %v, want ErrSyntax", err)
	}
	// nameIdx 0 with an illegal literal name.
	_, err = dec.Decode(Block{Instrs: []Instr{
		{Kind: KindLiteralIndexed, NameIdx: 0, Name: "Bad", Value: "v"},
	}})
	if !errors.Is(err, ErrSyntax) {
		t.Fatalf("err = %v, want ErrSyntax", err)
	}
	// oversized value is a syntax error.
	_, err = dec.Decode(Block{Instrs: []Instr{
		{Kind: KindLiteralNever, NameIdx: 0, Name: "k", Value: string(make([]byte, 1025))},
	}})
	if !errors.Is(err, ErrSyntax) {
		t.Fatalf("err = %v, want ErrSyntax", err)
	}
	// clean out-of-range name index is ErrIndex.
	_, err = dec.Decode(Block{Instrs: []Instr{
		{Kind: KindLiteralIndexed, NameIdx: 99, Value: "v"},
	}})
	if !errors.Is(err, ErrIndex) {
		t.Fatalf("err = %v, want ErrIndex", err)
	}
	// Indexed(0) is ErrIndex.
	_, err = dec.Decode(Block{Instrs: []Instr{{Kind: KindIndexed, Index: 0}}})
	if !errors.Is(err, ErrIndex) {
		t.Fatalf("err = %v, want ErrIndex", err)
	}
}

// Updates are validated before any is applied; violations are ErrUpdate
// and change nothing.
func TestUpdateValidation(t *testing.T) {
	dec := mustDecoder(t, 40, 50)
	for _, updates := range [][]int{{0}, {51}, {40, 0}, {65537}} {
		_, err := dec.Decode(Block{Updates: updates})
		if !errors.Is(err, ErrUpdate) {
			t.Fatalf("updates %v: err = %v, want ErrUpdate", updates, err)
		}
		if dec.Capacity() != 40 {
			t.Fatalf("capacity = %d, want 40 (unchanged)", dec.Capacity())
		}
	}
	out, err := dec.Decode(Block{Updates: []int{30, 45}})
	if err != nil || out != nil {
		t.Fatalf("valid updates: out=%v err=%v", out, err)
	}
	if dec.Capacity() != 45 {
		t.Fatalf("capacity = %d, want 45", dec.Capacity())
	}
}

// Invalid constructor/Resize/SetLimit/Encode arguments are ErrParam and
// leave all state untouched.
func TestParamValidation(t *testing.T) {
	for _, args := range [][2]int{{0, 10}, {10, 5}, {1, 65537}, {-1, 10}} {
		if _, err := NewEncoder(args[0], args[1], nil); !errors.Is(err, ErrParam) {
			t.Fatalf("NewEncoder%v: %v, want ErrParam", args, err)
		}
		if _, err := NewDecoder(args[0], args[1]); !errors.Is(err, ErrParam) {
			t.Fatalf("NewDecoder%v: %v, want ErrParam", args, err)
		}
	}
	enc := mustEncoder(t, 60, 100)
	mustEncode(t, enc, []Entry{{"k", "v"}})
	before := enc.Table()

	if err := enc.Resize(0); !errors.Is(err, ErrParam) {
		t.Fatalf("Resize(0): %v", err)
	}
	if err := enc.Resize(101); !errors.Is(err, ErrParam) {
		t.Fatalf("Resize(101): %v", err)
	}
	if err := enc.SetLimit(0); !errors.Is(err, ErrParam) {
		t.Fatalf("SetLimit(0): %v", err)
	}
	if err := enc.SetLimit(65537); !errors.Is(err, ErrParam) {
		t.Fatalf("SetLimit(65537): %v", err)
	}
	badNames := []string{"", "Bad", "a b", "a\x80b", fmt.Sprintf("%0257d", 0)}
	for _, n := range badNames {
		if _, err := enc.Encode([]Entry{{n, "v"}}); !errors.Is(err, ErrParam) {
			t.Fatalf("Encode name %q: %v, want ErrParam", n, err)
		}
	}
	if _, err := enc.Encode([]Entry{{"k", string(make([]byte, 1025))}}); !errors.Is(err, ErrParam) {
		t.Fatalf("Encode oversized value: %v, want ErrParam", err)
	}
	// A rejected Encode must not insert, nor touch base/min.
	if got := enc.Table(); !reflect.DeepEqual(got, before) {
		t.Fatalf("table = %v, want %v", got, before)
	}
	if err := enc.Resize(30); err != nil {
		t.Fatal(err)
	}
	b := mustEncode(t, enc, nil)
	if !reflect.DeepEqual(b.Updates, []int{30}) {
		t.Fatalf("updates = %v, want [30] (min/base untouched by rejected calls)", b.Updates)
	}
}

var (
	randNames = []string{
		"k", "a", "x", "n1", "n2", "zz",
		":method", ":path", ":scheme",
		"authorization",
	}
	randValues = []string{"GET", "POST", "/", "https", "v", "w", "1", "2", "", "s3cr3t"}
	nameChars  = "abcdefghijklmnopqrstuvwxyz0123456789-:_."
)

func randomHeaders(r *rand.Rand) []Entry {
	n := r.Intn(5)
	out := make([]Entry, 0, n)
	for i := 0; i < n; i++ {
		var name string
		switch r.Intn(3) {
		case 0, 1:
			name = randNames[r.Intn(len(randNames))]
		default:
			l := 1 + r.Intn(12)
			b := make([]byte, l)
			for j := range b {
				b[j] = nameChars[r.Intn(len(nameChars))]
			}
			name = string(b)
		}
		var value string
		if r.Intn(2) == 0 {
			value = randValues[r.Intn(len(randValues))]
		} else {
			l := r.Intn(40)
			b := make([]byte, l)
			for j := range b {
				b[j] = byte('a' + r.Intn(26))
			}
			value = string(b)
		}
		out = append(out, Entry{Name: name, Value: value})
	}
	return out
}

// Random header streams with random Resize/SetLimit sequences: after every
// block the decoder must reproduce the input headers and both dynamic
// tables (entries and capacity) must agree item by item.
func TestRandomConsistency(t *testing.T) {
	r := rand.New(rand.NewSource(20261003))
	enc := mustEncoder(t, 64, 256, "authorization")
	dec := mustDecoder(t, 64, 65536)

	for round := 0; round < 500; round++ {
		var ops []string
		for _, ro := range r.Perm(3)[:r.Intn(3)] {
			switch ro {
			case 0:
				c := 1 + r.Intn(300)
				err := enc.Resize(c)
				ops = append(ops, fmt.Sprintf("Resize(%d)->%v", c, err))
			case 1:
				l := 1 + r.Intn(70000)
				err := enc.SetLimit(l)
				ops = append(ops, fmt.Sprintf("SetLimit(%d)->%v", l, err))
			case 2:
				c := 1 + r.Intn(enc.Capacity()+100)
				err := enc.Resize(c)
				ops = append(ops, fmt.Sprintf("Resize(%d)->%v", c, err))
			}
		}
		headers := randomHeaders(r)
		b, err := enc.Encode(headers)
		if err != nil {
			t.Fatalf("round %d: Encode(%v): %v", round, headers, err)
		}
		out, err := dec.Decode(b)
		if err != nil {
			t.Fatalf("round %d: Decode: %v\nops=%v\nheaders=%v\nblock=%+v", round, err, ops, headers, b)
		}
		if len(headers) == 0 {
			if len(out) != 0 {
				t.Fatalf("round %d: decoded %v, want empty", round, out)
			}
		} else if !reflect.DeepEqual(out, headers) {
			t.Fatalf("round %d: decoded %v, want %v\nops=%v\nblock=%+v", round, out, headers, ops, b)
		}
		et, dt := enc.Table(), dec.Table()
		if !reflect.DeepEqual(et, dt) || enc.Capacity() != dec.Capacity() || enc.Used() != dec.Used() {
			t.Fatalf("round %d: divergence\nops=%v\nheaders=%v\nblock=%+v\nenc(t=%v,c=%d,u=%d)\ndec(t=%v,c=%d,u=%d)",
				round, ops, headers, b, et, enc.Capacity(), enc.Used(), dt, dec.Capacity(), dec.Used())
		}
		if round < 5 || round%100 == 0 {
			t.Logf("round %d: ops=%v headers=%v updates=%v instrs=%d -> table=%v cap=%d used=%d match=OK",
				round, ops, headers, b.Updates, len(b.Instrs), et, enc.Capacity(), enc.Used())
		}
	}
}

// Determinism: the same input sequence produces identical blocks.
func TestDeterminism(t *testing.T) {
	run := func() []Block {
		enc := mustEncoder(t, 64, 256, "authorization")
		r := rand.New(rand.NewSource(7))
		var blocks []Block
		for i := 0; i < 100; i++ {
			if i%3 == 0 {
				_ = enc.Resize(1 + r.Intn(256))
			}
			b := mustEncode(t, enc, randomHeaders(r))
			blocks = append(blocks, b)
		}
		return blocks
	}
	a, b := run(), run()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same input sequence produced different blocks")
	}
}

// Concurrent use: client-serialized Encode calls from many goroutines must
// all succeed, and decoding the blocks in the recorded serial order yields
// matching final tables.
func TestConcurrentUse(t *testing.T) {
	enc := mustEncoder(t, 128, 512, "authorization")
	dec := mustDecoder(t, 128, 65536)

	var mu sync.Mutex
	var blocks []Block
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 50; i++ {
				// The mutex serializes Encode+record so the recorded order
				// is a valid serial order of the internal critical sections.
				mu.Lock()
				if r.Intn(4) == 0 {
					_ = enc.Resize(1 + r.Intn(512))
				}
				b, err := enc.Encode(randomHeaders(r))
				if err == nil {
					blocks = append(blocks, b)
				}
				mu.Unlock()
				if err != nil {
					t.Errorf("Encode: %v", err)
					return
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()

	for i, b := range blocks {
		if _, err := dec.Decode(b); err != nil {
			t.Fatalf("block %d: Decode: %v", i, err)
		}
	}
	checkTables(t, enc, dec)
	t.Logf("decoded %d concurrently produced blocks; final tables match (cap=%d used=%d)",
		len(blocks), enc.Capacity(), enc.Used())
}
