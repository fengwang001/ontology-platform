package hpack

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func mustNewEncoder(t *testing.T, c0, l0 int, sensitive ...string) *Encoder {
	t.Helper()
	sens := map[string]bool{}
	for _, s := range sensitive {
		sens[s] = true
	}
	e, err := NewEncoder(c0, l0, sens)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	return e
}

func checkTables(t *testing.T, enc *Encoder, dec *Decoder, ctx string) {
	t.Helper()
	ee, de := enc.Entries(), dec.Entries()
	if !reflect.DeepEqual(ee, de) {
		t.Fatalf("%s: dynamic tables diverge\nenc=%v\ndec=%v", ctx, ee, de)
	}
	if enc.Capacity() != dec.Capacity() {
		t.Fatalf("%s: capacities diverge enc=%d dec=%d", ctx, enc.Capacity(), dec.Capacity())
	}
}

func roundtrip(t *testing.T, enc *Encoder, dec *Decoder, headers []Header, ctx string) Block {
	t.Helper()
	block, err := enc.Encode(headers)
	if err != nil {
		t.Fatalf("%s: Encode: %v", ctx, err)
	}
	got, err := dec.Decode(block)
	if err != nil {
		t.Fatalf("%s: Decode: %v block=%v", ctx, err, block)
	}
	if len(got) != len(headers) || (len(headers) > 0 && !reflect.DeepEqual(got, headers)) {
		t.Fatalf("%s: headers mismatch\nwant=%v\ngot =%v", ctx, headers, got)
	}
	checkTables(t, enc, dec, ctx)
	return block
}

// Empty table, C=60, first (k,v) => literal with literal name, size 34.
func TestExampleFirstLiteral(t *testing.T) {
	enc := mustNewEncoder(t, 60, 60)
	dec, _ := NewDecoder(60, 60)
	block := roundtrip(t, enc, dec, []Header{{"k", "v"}}, "first literal")
	want := Block{Instrs: []Instruction{LiteralIndexed{NameIdx: 0, Name: "k", Value: "v"}}}
	if !reflect.DeepEqual(block, want) {
		t.Fatalf("block=%v want=%v", block, want)
	}
	if got := enc.Entries(); !reflect.DeepEqual(got, []Header{{"k", "v"}}) {
		t.Fatalf("entries=%v", got)
	}
	t.Logf("input=[(k,v)] output=%v reason=no exact/name match => NameIdx 0; size=1+1+32=34<=60 inserted", block.Instrs)
}

// Second (k,w): name-only match at dynamic index 5; 34+34=68>60 evicts (k,v).
// Decoder resolves the name at index 5 before its own eviction/insert.
func TestExampleNameMatchEviction(t *testing.T) {
	enc := mustNewEncoder(t, 60, 60)
	dec, _ := NewDecoder(60, 60)
	roundtrip(t, enc, dec, []Header{{"k", "v"}}, "setup")
	block := roundtrip(t, enc, dec, []Header{{"k", "w"}}, "name match + evict")
	want := Block{Instrs: []Instruction{LiteralIndexed{NameIdx: 5, Name: "", Value: "w"}}}
	if !reflect.DeepEqual(block, want) {
		t.Fatalf("block=%v want=%v", block, want)
	}
	if got := enc.Entries(); !reflect.DeepEqual(got, []Header{{"k", "w"}}) {
		t.Fatalf("entries=%v", got)
	}
	t.Logf("input=[(k,w)] output=%v reason=name-only match newest dynamic 5; 34+34=68>60 evict (k,v); decoder reads index 5 pre-insert", block.Instrs)
}

// Entry larger than capacity => literal emitted, table cleared, not inserted.
func TestExampleOversizeClears(t *testing.T) {
	enc := mustNewEncoder(t, 40, 40)
	dec, _ := NewDecoder(40, 40)
	roundtrip(t, enc, dec, []Header{{"small", "x"}}, "seed")
	block := roundtrip(t, enc, dec, []Header{{"abcdefgh", "ijklmnopq"}}, "oversize")
	wantInstr := LiteralIndexed{NameIdx: 0, Name: "abcdefgh", Value: "ijklmnopq"}
	if !reflect.DeepEqual(block.Instrs, []Instruction{wantInstr}) {
		t.Fatalf("instrs=%v", block.Instrs)
	}
	if len(enc.Entries()) != 0 {
		t.Fatalf("table should be cleared, got %v", enc.Entries())
	}
	t.Logf("input=[(abcdefgh,ijklmnopq)] output=%v reason=size 8+9+32=49>C 40 => clear table, do not insert", block.Instrs)
}

// Static full match wins over a dynamic full match.
func TestStaticFullMatchWins(t *testing.T) {
	enc := mustNewEncoder(t, 60, 60)
	dec, _ := NewDecoder(60, 60)
	roundtrip(t, enc, dec, []Header{{":method", "GET"}}, "first insert dynamic")
	block, err := enc.Encode([]Header{{":method", "GET"}})
	if err != nil {
		t.Fatal(err)
	}
	idx, ok := block.Instrs[0].(Indexed)
	if !ok || idx.Index != 1 {
		t.Fatalf("want Indexed(1), got %v", block.Instrs[0])
	}
	got, err := dec.Decode(block)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []Header{{":method", "GET"}}) {
		t.Fatalf("got %v", got)
	}
	checkTables(t, enc, dec, "static wins")
	t.Logf("output=%v reason=exact static index 1 preferred over dynamic exact match", block.Instrs)
}

// Name-only match picks the smallest static index (:method => 1).
func TestStaticNameMatchSmallest(t *testing.T) {
	enc := mustNewEncoder(t, 60, 60)
	dec, _ := NewDecoder(60, 60)
	roundtrip(t, enc, dec, []Header{{":method", "POST"}}, "seed POST")
	block := roundtrip(t, enc, dec, []Header{{":method", "DELETE"}}, "new value same name")
	want := LiteralIndexed{NameIdx: 1, Name: "", Value: "DELETE"}
	if !reflect.DeepEqual(block.Instrs, []Instruction{want}) {
		t.Fatalf("instrs=%v want %v", block.Instrs, want)
	}
	t.Logf("output=%v reason=name-only match uses smallest static index 1", block.Instrs)
}

// Sensitive name => LiteralNever, not inserted on either side. The name index
// is still resolved: unknown name => 0 with literal name; static name => 1.
func TestSensitiveNeverIndexed(t *testing.T) {
	enc := mustNewEncoder(t, 60, 60, "secret", ":method")
	dec, _ := NewDecoder(60, 60)
	block := roundtrip(t, enc, dec, []Header{{"secret", "a"}}, "sensitive literal name")
	if !reflect.DeepEqual(block.Instrs, []Instruction{LiteralNever{NameIdx: 0, Name: "secret", Value: "a"}}) {
		t.Fatalf("instrs=%v", block.Instrs)
	}
	if len(enc.Entries()) != 0 {
		t.Fatalf("sensitive must not be inserted: %v", enc.Entries())
	}
	roundtrip(t, enc, dec, []Header{{"alpha", "x"}}, "insert alpha, must remain the only entry")
	block = roundtrip(t, enc, dec, []Header{{":method", "PATCH"}}, "sensitive static name keeps name index 1")
	if !reflect.DeepEqual(block.Instrs, []Instruction{LiteralNever{NameIdx: 1, Name: "", Value: "PATCH"}}) {
		t.Fatalf("instrs=%v", block.Instrs)
	}
	if got := enc.Entries(); !reflect.DeepEqual(got, []Header{{"alpha", "x"}}) {
		t.Fatalf("sensitive must not insert: %v", got)
	}
	t.Logf("outputs=%v reason=sensitive => LiteralNever, name index still resolved (0 literal / 1 static), no insert", block.Instrs)
}

// Shrink then grow back to base within one gap => two updates.
func TestUpdatesDownThenUp(t *testing.T) {
	enc := mustNewEncoder(t, 60, 100)
	dec, _ := NewDecoder(60, 100)
	if err := enc.Resize(10); err != nil {
		t.Fatal(err)
	}
	if err := enc.Resize(60); err != nil {
		t.Fatal(err)
	}
	block := roundtrip(t, enc, dec, nil, "updates [10,60]")
	if !reflect.DeepEqual(block.Updates, []int{10, 60}) {
		t.Fatalf("updates=%v want [10 60]", block.Updates)
	}
	t.Logf("updates=%v reason=mn=10<base=60 and mn<cur=60 => [mn,cur]", block.Updates)
}

func TestUpdatesShrinkOnly(t *testing.T) {
	enc := mustNewEncoder(t, 60, 100)
	dec, _ := NewDecoder(60, 100)
	if err := enc.Resize(10); err != nil {
		t.Fatal(err)
	}
	block := roundtrip(t, enc, dec, nil, "updates [10]")
	if !reflect.DeepEqual(block.Updates, []int{10}) {
		t.Fatalf("updates=%v", block.Updates)
	}
}

func TestUpdatesGrowOnly(t *testing.T) {
	enc := mustNewEncoder(t, 60, 100)
	dec, _ := NewDecoder(60, 100)
	if err := enc.Resize(80); err != nil {
		t.Fatal(err)
	}
	block := roundtrip(t, enc, dec, nil, "updates [80]")
	if !reflect.DeepEqual(block.Updates, []int{80}) {
		t.Fatalf("updates=%v", block.Updates)
	}
	t.Logf("updates=%v reason=only grew within limit, cur!=base => [cur]", block.Updates)
}

func TestSetLimitShrinksAndNegotiates(t *testing.T) {
	enc := mustNewEncoder(t, 60, 100)
	dec, _ := NewDecoder(60, 100)
	roundtrip(t, enc, dec, []Header{{"k", "v"}}, "seed 34 bytes")
	if err := enc.SetLimit(30); err != nil {
		t.Fatal(err)
	}
	if enc.Capacity() != 30 || len(enc.Entries()) != 0 {
		t.Fatalf("SetLimit should shrink C and evict: C=%d entries=%v", enc.Capacity(), enc.Entries())
	}
	block := roundtrip(t, enc, dec, nil, "setlimit sync")
	if !reflect.DeepEqual(block.Updates, []int{30}) {
		t.Fatalf("updates=%v", block.Updates)
	}
	if err := enc.Resize(40); err == nil {
		t.Fatalf("Resize above limit must fail")
	}
}

func TestUpdatesEmptyWhenUnchanged(t *testing.T) {
	enc := mustNewEncoder(t, 60, 100)
	dec, _ := NewDecoder(60, 100)
	block := roundtrip(t, enc, dec, []Header{{"k", "v"}}, "first")
	if len(block.Updates) != 0 {
		t.Fatalf("updates=%v", block.Updates)
	}
	block = roundtrip(t, enc, dec, []Header{{"k", "v2"}}, "second")
	if len(block.Updates) != 0 {
		t.Fatalf("updates=%v", block.Updates)
	}
}

// Decoder error on the third instruction rolls back the first two inserts.
func TestDecodeRollbackThirdInstruction(t *testing.T) {
	enc := mustNewEncoder(t, 300, 300)
	dec, _ := NewDecoder(300, 300)
	roundtrip(t, enc, dec, []Header{{"a", "1"}, {"b", "2"}}, "two inserts land on both sides")

	bad := Block{Instrs: []Instruction{
		LiteralIndexed{NameIdx: 0, Name: "c", Value: "3"},
		LiteralIndexed{NameIdx: 0, Name: "d", Value: "4"},
		Indexed{Index: 999},
	}}
	before := dec.Entries()
	got, err := dec.Decode(bad)
	if err != ErrIndex {
		t.Fatalf("want ErrIndex, got %v", err)
	}
	if got != nil {
		t.Fatalf("failed block must emit no headers, got %v", got)
	}
	if !reflect.DeepEqual(dec.Entries(), before) {
		t.Fatalf("rollback failed\nbefore=%v\nafter =%v", before, dec.Entries())
	}
	if dec.Capacity() != 300 {
		t.Fatalf("capacity changed: %d", dec.Capacity())
	}
	t.Logf("input=%v => error on third instruction; table rolled back to %v; reason=index 999 out of range", bad.Instrs, before)
}

func TestDecodeErrorClasses(t *testing.T) {
	dec, _ := NewDecoder(60, 60)

	// ErrUpdate: update out of advertised range; full rollback.
	roundtripSeed(t, dec, []Header{{"a", "1"}})
	before := dec.Entries()
	if _, err := dec.Decode(Block{Updates: []int{61}}); err != ErrUpdate {
		t.Fatalf("want ErrUpdate, got %v", err)
	}
	if _, err := dec.Decode(Block{Updates: []int{0}}); err != ErrUpdate {
		t.Fatalf("want ErrUpdate for 0, got %v", err)
	}
	if !reflect.DeepEqual(dec.Entries(), before) {
		t.Fatalf("ErrUpdate rollback failed: %v vs %v", dec.Entries(), before)
	}

	// ErrIndex: index 0.
	if _, err := dec.Decode(Block{Instrs: []Instruction{Indexed{Index: 0}}}); err != ErrIndex {
		t.Fatalf("want ErrIndex, got %v", err)
	}
	// ErrSyntax beats ErrIndex: nameIdx>0 must not carry a literal name.
	if _, err := dec.Decode(Block{Instrs: []Instruction{
		LiteralIndexed{NameIdx: 999, Name: "x", Value: "y"},
	}}); err != ErrSyntax {
		t.Fatalf("want ErrSyntax precedence, got %v", err)
	}
	// nameIdx 0 without a valid literal name.
	if _, err := dec.Decode(Block{Instrs: []Instruction{
		LiteralIndexed{NameIdx: 0, Name: "Bad Name", Value: "y"},
	}}); err != ErrSyntax {
		t.Fatalf("want ErrSyntax invalid name, got %v", err)
	}
	// value too long.
	long := make([]byte, 1025)
	if _, err := dec.Decode(Block{Instrs: []Instruction{
		LiteralIndexed{NameIdx: 0, Name: "ok", Value: string(long)},
	}}); err != ErrSyntax {
		t.Fatalf("want ErrSyntax long value, got %v", err)
	}
	// nameIdx out of range with empty literal => ErrIndex.
	if _, err := dec.Decode(Block{Instrs: []Instruction{
		LiteralIndexed{NameIdx: 999, Name: "", Value: "y"},
	}}); err != ErrIndex {
		t.Fatalf("want ErrIndex, got %v", err)
	}
	// Failed block changed nothing.
	if !reflect.DeepEqual(dec.Entries(), before) {
		t.Fatalf("rollback failed: %v vs %v", dec.Entries(), before)
	}
	t.Logf("error precedence verified: ErrSyntax before ErrIndex; all failures rolled back to %v", before)
}

func roundtripSeed(t *testing.T, dec *Decoder, headers []Header) {
	t.Helper()
	enc := mustNewEncoder(t, dec.Capacity(), 65536)
	enc.table.restore(dec.table.snapshot(), dec.Capacity())
	block, err := enc.Encode(headers)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decode(block); err != nil {
		t.Fatal(err)
	}
}

func TestEncoderInvalidArgumentsLeaveStateUntouched(t *testing.T) {
	enc := mustNewEncoder(t, 60, 100)
	dec, _ := NewDecoder(60, 100)
	roundtrip(t, enc, dec, []Header{{"k", "v"}}, "seed")
	before := enc.Entries()

	if _, err := NewEncoder(0, 10, nil); err != ErrInvalidArgument {
		t.Fatalf("c0<1: %v", err)
	}
	if _, err := NewEncoder(11, 10, nil); err != ErrInvalidArgument {
		t.Fatalf("c0>l0: %v", err)
	}
	if _, err := NewEncoder(1, 65537, nil); err != ErrInvalidArgument {
		t.Fatalf("l0>65536: %v", err)
	}
	if _, err := NewDecoder(1, 65537); err != ErrInvalidArgument {
		t.Fatalf("ld>65536: %v", err)
	}
	if err := enc.SetLimit(0); err != ErrInvalidArgument {
		t.Fatalf("SetLimit(0): %v", err)
	}
	if err := enc.SetLimit(65537); err != ErrInvalidArgument {
		t.Fatalf("SetLimit(65537): %v", err)
	}
	if err := enc.Resize(0); err != ErrInvalidArgument {
		t.Fatalf("Resize(0): %v", err)
	}
	if err := enc.Resize(101); err != ErrInvalidArgument {
		t.Fatalf("Resize above limit: %v", err)
	}

	// Invalid header aborts the whole Encode: no inserts, base/mn unchanged.
	if _, err := enc.Encode([]Header{{"ok", "1"}, {"Bad", "2"}}); err != ErrInvalidArgument {
		t.Fatalf("invalid name: %v", err)
	}
	long := make([]byte, 1025)
	if _, err := enc.Encode([]Header{{"ok", string(long)}}); err != ErrInvalidArgument {
		t.Fatalf("invalid value: %v", err)
	}
	if !reflect.DeepEqual(enc.Entries(), before) {
		t.Fatalf("entries changed after rejected calls: %v vs %v", enc.Entries(), before)
	}
	if enc.Capacity() != 60 {
		t.Fatalf("capacity changed: %d", enc.Capacity())
	}
	// Next block still carries no updates (base/mn untouched).
	block, err := enc.Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Updates) != 0 {
		t.Fatalf("base/mn changed after rejected Encode: updates=%v", block.Updates)
	}
}

const randChars = "abcdefghijklmnopqrstuvwxyz0123456789-:_. "

func randName(r *rand.Rand) string {
	n := 1 + r.Intn(12)
	b := make([]byte, n)
	for i := range b {
		c := randChars[r.Intn(len(randChars)-1)] // exclude trailing space
		b[i] = c
	}
	return string(b)
}

func randValue(r *rand.Rand) string {
	n := r.Intn(14)
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + r.Intn(26))
	}
	return string(b)
}

// Random streams with random Resize/SetLimit: every produced block is decoded
// and the two dynamic tables are compared entry-by-entry after each block.
func TestRandomStreamTableParity(t *testing.T) {
	const iterations = 40
	for seed := int64(1); seed <= 8; seed++ {
		r := rand.New(rand.NewSource(seed))
		enc := mustNewEncoder(t, 64, 128)
		dec, _ := NewDecoder(64, 128)
		t.Logf("seed=%d start C=64 L=128", seed)
		for step := 0; step < iterations; step++ {
			switch r.Intn(3) {
			case 0:
				c := 1 + r.Intn(enc.limit)
				if err := enc.Resize(c); err != nil {
					t.Fatalf("seed=%d step=%d Resize(%d): %v", seed, step, c, err)
				}
				t.Logf("seed=%d step=%d op=Resize(%d) encC=%d entries=%v", seed, step, c, enc.Capacity(), enc.Entries())
			case 1:
				l := 1 + r.Intn(128)
				if err := enc.SetLimit(l); err != nil {
					t.Fatalf("seed=%d step=%d SetLimit(%d): %v", seed, step, l, err)
				}
				t.Logf("seed=%d step=%d op=SetLimit(%d) encC=%d entries=%v", seed, step, l, enc.Capacity(), enc.Entries())
			default:
				n := r.Intn(5)
				headers := make([]Header, n)
				for i := range headers {
					headers[i] = Header{Name: randName(r), Value: randValue(r)}
				}
				block, err := enc.Encode(headers)
				if err != nil {
					t.Fatalf("seed=%d step=%d Encode: %v", seed, step, err)
				}
				got, err := dec.Decode(block)
				if err != nil {
					t.Fatalf("seed=%d step=%d Decode: %v block=%+v", seed, step, err, block)
				}
				if !reflect.DeepEqual(got, headers) {
					t.Fatalf("seed=%d step=%d headers mismatch\nwant=%v\ngot =%v", seed, step, headers, got)
				}
				checkTables(t, enc, dec, "random parity")
				t.Logf("seed=%d step=%d input=%v updates=%v instrs=%v => enc=%v dec=%v MATCH",
					seed, step, headers, block.Updates, describe(block.Instrs), enc.Entries(), dec.Entries())
			}
		}
	}
}

func describe(instrs []Instruction) []string {
	out := make([]string, 0, len(instrs))
	for _, in := range instrs {
		switch p := in.(type) {
		case Indexed:
			out = append(out, fmt.Sprintf("Indexed(%d)", p.Index))
		case LiteralIndexed:
			out = append(out, fmt.Sprintf("LiteralIndexed(%d,%q,%q)", p.NameIdx, p.Name, p.Value))
		case LiteralNever:
			out = append(out, fmt.Sprintf("LiteralNever(%d,%q,%q)", p.NameIdx, p.Name, p.Value))
		}
	}
	return out
}

// Determinism: identical input sequences yield identical blocks and tables.
func TestDeterministicBlocks(t *testing.T) {
	build := func() ([]Block, []Header) {
		r := rand.New(rand.NewSource(42))
		enc := mustNewEncoder(t, 64, 128)
		var blocks []Block
		var final []Header
		for step := 0; step < 20; step++ {
			switch r.Intn(3) {
			case 0:
				_ = enc.Resize(1 + r.Intn(enc.limit))
			case 1:
				_ = enc.SetLimit(1 + r.Intn(200))
			default:
				headers := []Header{{Name: randName(r), Value: randValue(r)}}
				b, err := enc.Encode(headers)
				if err != nil {
					t.Fatal(err)
				}
				blocks = append(blocks, b)
				final = enc.Entries()
			}
		}
		return blocks, final
	}
	b1, e1 := build()
	b2, e2 := build()
	if !reflect.DeepEqual(b1, b2) {
		t.Fatalf("blocks not deterministic\n%v\n%v", b1, b2)
	}
	if !reflect.DeepEqual(e1, e2) {
		t.Fatalf("final tables differ\n%v\n%v", e1, e2)
	}
}

// Concurrent calls must be race-free and equivalent to some serial order:
// after N concurrent Encodes of equal-sized entries, invariants must hold.
func TestConcurrentAccess(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			enc := mustNewEncoder(t, 256, 256)
			dec, _ := NewDecoder(256, 256)
			for i := 0; i < 25; i++ {
				h := []Header{{Name: "hdr", Value: fmt.Sprintf("g%di%d", g, i)}}
				block, err := enc.Encode(h)
				if err != nil {
					t.Errorf("Encode: %v", err)
					return
				}
				if _, err := dec.Decode(block); err != nil {
					t.Errorf("Decode: %v", err)
					return
				}
			}
			checkTables(t, enc, dec, fmt.Sprintf("goroutine %d", g))
		}(g)
	}
	wg.Wait()
}
