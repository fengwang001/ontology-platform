package merge

import (
	"math"
	"testing"

	"ontology/record"
)

func drain(m *Merger) []record.Record {
	var out []record.Record
	for {
		r, ok := m.Next()
		if !ok {
			return out
		}
		out = append(out, r)
	}
}

func assertSorted(t *testing.T, out []record.Record) {
	t.Helper()
	for i := 1; i < len(out); i++ {
		if out[i].Key < out[i-1].Key {
			t.Fatalf("pos %d out of order: %q after %q", i, out[i].Key, out[i-1].Key)
		}
	}
}

func TestEqualKeyAcrossThreeRuns(t *testing.T) {
	// Same key split across three runs. Within each run the records for
	// the key carry small run-local sequence numbers (0,1,...); the
	// value bytes tag the run so the test can assert global arrival
	// order. A merge keyed on local sequence alone would interleave the
	// runs; the correct tie-break is run creation order.
	recs := func(tag byte, locals ...uint64) []record.Record {
		out := make([]record.Record, len(locals))
		for i, s := range locals {
			out[i] = record.Record{Key: "dup", Seq: s, Value: []byte{tag}}
		}
		return out
	}
	sources := []Source{
		{RunID: 1, Records: recs(1, 0, 1)},
		{RunID: 2, Records: append(recs(2, 0), record.Record{Key: "z", Seq: 5})},
		{RunID: 3, Records: recs(3, 0, 1, 2)},
	}
	out := drain(New(sources))
	assertSorted(t, out)
	if len(out) != 7 || out[6].Key != "z" {
		t.Fatalf("bad output: %+v", out)
	}
	gotTags := []byte{}
	for _, r := range out {
		if r.Key == "dup" {
			gotTags = append(gotTags, r.Value[0])
		}
	}
	wantTags := []byte{1, 1, 2, 3, 3, 3}
	if string(gotTags) != string(wantTags) {
		t.Fatalf("equal-key order = %v want %v", gotTags, wantTags)
	}
}

func TestComparisonBound(t *testing.T) {
	K, per := 7, 1000
	N := K * per
	var srcs []Source
	for r := 0; r < K; r++ {
		rs := make([]record.Record, per)
		for i := 0; i < per; i++ {
			n := i*K + r
			rs[i] = record.Record{Key: keyOf(n), Seq: uint64(n)}
		}
		srcs = append(srcs, Source{RunID: uint64(r + 1), Records: rs})
	}
	m := New(srcs)
	out := drain(m)
	assertSorted(t, out)
	if len(out) != N {
		t.Fatalf("count %d want %d", len(out), N)
	}
	logBound := math.Ceil(math.Log2(float64(K + 1)))
	bound := uint64(4 * float64(N) * logBound)
	if got := m.Comparisons(); got > bound {
		t.Fatalf("comparisons %d exceed bound %d", got, bound)
	}
}

func keyOf(i int) string {
	b := []byte("k0000000000")
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b)
}

func TestEmptyAndSingle(t *testing.T) {
	if _, ok := New(nil).Next(); ok {
		t.Fatal("empty merge produced records")
	}
	m := New([]Source{{RunID: 1, Records: []record.Record{{Key: "x", Seq: 1}}}})
	r, ok := m.Next()
	if !ok || r.Key != "x" {
		t.Fatal("single record merge failed")
	}
	if _, ok := m.Next(); ok {
		t.Fatal("extra record")
	}
}
