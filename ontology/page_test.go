package ontology

import (
	"bytes"
	"errors"
	"testing"
)

func TestZeroWidthPageD1(t *testing.T) {
	w, _ := New(10, 100)
	mustAppend(t, w, dupu(42, 5)...)
	p, err := w.Flush()
	if err != nil {
		t.Fatal(err)
	}
	if p.Enc != EncDict || p.Width != 0 || p.DictLen != 1 {
		t.Fatalf("page=%+v", p)
	}
	if !bytes.Equal(p.Data, []byte{0, 3}) {
		t.Fatalf("data=% x", p.Data)
	}
	out, err := w.Decode(p)
	if err != nil {
		t.Fatal(err)
	}
	if !u32Equal(out, dupu(42, 5)) {
		t.Fatalf("decode=%v", out)
	}
}

func TestDEqualMaxDictAndOverflow(t *testing.T) {
	w, _ := New(2, 100)

	mustAppend(t, w, dupu(7, 20)...)
	p, err := w.Flush()
	if err != nil {
		t.Fatal(err)
	}
	if p.Enc != EncDict || p.DictLen != 1 {
		t.Fatalf("page=%+v", p)
	}

	mustAppend(t, w, dupu(8, 20)...)
	p, err = w.Flush()
	if err != nil {
		t.Fatal(err)
	}
	if p.Enc != EncDict || p.Width != 1 || p.DictLen != 2 {
		t.Fatalf("page=%+v", p)
	}

	// Third distinct value: D=3 > maxDict=2 -> sticky fallback immediately.
	mustAppend(t, w, dupu(9, 20)...)
	p, err = w.Flush()
	if err != nil {
		t.Fatal(err)
	}
	if p.Enc != EncPlain || p.Width != 0 || p.DictLen != 0 {
		t.Fatalf("page=%+v", p)
	}
	if !w.fallback || w.miss != 0 {
		t.Fatalf("fallback=%v miss=%d", w.fallback, w.miss)
	}

	mustAppend(t, w, dupu(10, 20)...)
	p, err = w.Flush()
	if err != nil {
		t.Fatal(err)
	}
	if p.Enc != EncPlain {
		t.Fatal("fallback is not sticky")
	}
	if len(w.dictVals) != 2 {
		t.Fatalf("dict grew after fallback: %d", len(w.dictVals))
	}

	// A Dict page written earlier still decodes with stable indices.
	out, err := w.Decode(Page{
		Enc: EncDict, Rows: 20, Width: 1, DictLen: 2,
		Data: []byte{1, 0x28, 0x00},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !u32Equal(out, dupu(7, 20)) {
		t.Fatalf("old page decode=%v", out)
	}
}

func TestDictPageTiesPlain(t *testing.T) {
	type tie struct{ rows, distinct int }
	var found tie
search:
	for n := 2; n <= 2000; n++ {
		wid := widthForDistinct(n)
		for r := n; r <= 2000; r++ {
			groups := (r + 7) / 8
			h := lenUvarint(groups<<1|1) + groups*wid
			if 1+h+4*n == 4*r {
				found = tie{r, n}
				break search
			}
		}
	}
	if found.rows == 0 {
		t.Fatal("no tie configuration found")
	}
	t.Logf("tie config rows=%d distinct=%d", found.rows, found.distinct)

	w, _ := New(65536, 65536)
	seq := tieSequence(found.rows, found.distinct)
	mustAppend(t, w, seq...)
	p, err := w.Flush()
	if err != nil {
		t.Fatal(err)
	}
	if p.Enc != EncPlain {
		t.Fatalf("enc=%v want Plain on tie", p.Enc)
	}
	if len(w.dictVals) != 0 || w.miss != 1 {
		t.Fatalf("dict grew on tie: len=%d miss=%d", len(w.dictVals), w.miss)
	}

	// Values lost on a size decision are reallocated when they appear later.
	mustAppend(t, w, dupu(seq[0], 30)...)
	p, err = w.Flush()
	if err != nil {
		t.Fatal(err)
	}
	if p.Enc != EncDict || p.DictLen != 1 {
		t.Fatalf("page=%+v", p)
	}
	out, err := w.Decode(p)
	if err != nil {
		t.Fatal(err)
	}
	if !u32Equal(out, dupu(seq[0], 30)) {
		t.Fatal("decode mismatch after reallocation")
	}
}

func TestThreeMissesStickyWithDictReset(t *testing.T) {
	w, _ := New(65536, 65536)

	flushUniques := func(n int) {
		t.Helper()
		vs := make([]uint32, n)
		for i := range vs {
			vs[i] = uint32(i) + 1
		}
		mustAppend(t, w, vs...)
		p, err := w.Flush()
		if err != nil {
			t.Fatal(err)
		}
		if p.Enc != EncPlain {
			t.Fatalf("enc=%v want Plain", p.Enc)
		}
	}

	flushUniques(4)
	if w.fallback || w.miss != 1 {
		t.Fatalf("miss=%d fallback=%v", w.miss, w.fallback)
	}
	flushUniques(5)
	if w.fallback || w.miss != 2 {
		t.Fatalf("miss=%d fallback=%v", w.miss, w.fallback)
	}

	mustAppend(t, w, dupu(77, 30)...)
	p, err := w.Flush()
	if err != nil {
		t.Fatal(err)
	}
	if p.Enc != EncDict || w.miss != 0 {
		t.Fatalf("enc=%v miss=%d", p.Enc, w.miss)
	}

	flushUniques(6)
	flushUniques(7)
	if w.fallback {
		t.Fatal("fell back before third miss")
	}
	flushUniques(8)
	if !w.fallback {
		t.Fatal("third miss did not set fallback")
	}

	mustAppend(t, w, dupu(78, 10)...)
	p, err = w.Flush()
	if err != nil || p.Enc != EncPlain {
		t.Fatalf("after fallback page=%+v err=%v", p, err)
	}
}

func TestRejectedOpsDoNotMutate(t *testing.T) {
	w, _ := New(10, 1)
	mustAppend(t, w, 1)
	before := append([]uint32(nil), w.buf...)
	dictBefore := len(w.dictVals)
	if err := w.Append(2); !errors.Is(err, ErrFull) {
		t.Fatal(err)
	}
	if !u32Equal(w.buf, before) || len(w.dictVals) != dictBefore || w.fallback {
		t.Fatal("rejected append mutated state")
	}
	p1, _ := w.Flush()
	if _, err := w.Flush(); !errors.Is(err, ErrEmpty) {
		t.Fatal(err)
	}
	if len(w.buf) != 0 || len(w.dictVals) != p1.DictLen {
		t.Fatal("empty flush mutated state")
	}
}

func widthForDistinct(d int) int {
	if d <= 1 {
		return 0
	}
	w := 1
	for (1 << w) < d {
		w++
	}
	return w
}
