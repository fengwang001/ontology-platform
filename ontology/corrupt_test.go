package ontology

import (
	"errors"
	"testing"
)

func TestCorruptPages(t *testing.T) {
	w, _ := New(256, 200)

	// Eight 1s then two 2s => one RLE run then one bit-packed group (6 pads).
	mustAppend(t, w, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2)
	good, err := w.Flush()
	if err != nil {
		t.Fatal(err)
	}
	if good.Enc != EncDict {
		t.Fatalf("want Dict page, got %+v", good)
	}
	out, err := w.Decode(good)
	if err != nil {
		t.Fatal(err)
	}
	if !u32Equal(out, []uint32{1, 1, 1, 1, 1, 1, 1, 1, 2, 2}) {
		t.Fatalf("decode=%v", out)
	}

	bad := func(fn func([]byte) []byte) {
		t.Helper()
		data := fn(append([]byte(nil), good.Data...))
		p := good
		p.Data = data
		if _, err := w.Decode(p); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("want ErrCorrupt for % x, got %v", data, err)
		}
	}

	bad(func(b []byte) []byte { return b[:len(b)-1] })   // truncated
	bad(func(b []byte) []byte { return append(b, 0) })   // trailing byte
	bad(func(b []byte) []byte { b[0] = 2; return b })    // wrong width
	bad(func(b []byte) []byte { b[0] = 0; return b })    // zero width mismatch
	bad(func(b []byte) []byte { b[1] = 0x02; return b }) // RLE count zero
	bad(func(b []byte) []byte {
		return append(b[:1], []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02}...)
	}) // non-canonical overflow varint
	bad(func(b []byte) []byte {
		return append(b[:1], []byte{0x90, 0x00, 0x00, 0x03, 0x03}...)
	}) // non-minimal varint
	bad(func(b []byte) []byte { b[1] = 0x30; return b }) // RLE count exceeds rows
	bad(func(b []byte) []byte { b[3] = 0x05; return b }) // groups exceed rows

	// Padding positions must hold index 0: flip a pad bit on (w=2, group byte).
	bad(func(b []byte) []byte { b[len(b)-1] |= 0xC0; return b })

	// Metadata mismatches.
	for _, mut := range []func(*Page){
		func(p *Page) { p.Rows = 9 },  // claimed padding slot holds nonzero
		func(p *Page) { p.Rows = 17 }, // groups cannot supply that many
		func(p *Page) { p.DictLen = 3 },
		func(p *Page) { p.Width = 9 },
		func(p *Page) { p.Enc = EncPlain },
	} {
		p := good
		mut(&p)
		if _, err := w.Decode(p); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("metadata case %+v: want ErrCorrupt got %v", p, err)
		}
	}

	// Plain checks.
	if _, err := w.Decode(Page{Enc: EncPlain, Rows: 2, Data: make([]byte, 7)}); !errors.Is(err, ErrCorrupt) {
		t.Fatal("plain short data")
	}
	if _, err := w.Decode(Page{Enc: EncPlain, Rows: 1, Width: 1, Data: make([]byte, 4)}); !errors.Is(err, ErrCorrupt) {
		t.Fatal("plain with nonzero width accepted")
	}
	if _, err := w.Decode(Page{Enc: EncPlain, Rows: 1, DictLen: 1, Data: make([]byte, 4)}); !errors.Is(err, ErrCorrupt) {
		t.Fatal("plain with nonzero dictlen accepted")
	}

	// Unknown encoding.
	if _, err := w.Decode(Page{Enc: Encoding(9), Rows: 0, Data: nil}); !errors.Is(err, ErrCorrupt) {
		t.Fatal("unknown encoding accepted")
	}

	// Index beyond DictLen but within width.
	p := good
	p.DictLen = 1
	if _, err := w.Decode(p); !errors.Is(err, ErrCorrupt) {
		t.Fatal("index >= DictLen accepted")
	}

}
