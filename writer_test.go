package ontology

import (
	"bytes"
	"errors"
	"testing"
)

func mustClose(t *testing.T, w *Writer) {
	t.Helper()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func flushBlock(t *testing.T, w *Writer, p []byte) {
	t.Helper()
	if _, err := w.Write(p); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestNewWriterParams(t *testing.T) {
	for _, ps := range [][2]int{{0, 1}, {1, 0}, {-1, 2}, {3, -1}} {
		if _, err := NewWriter(ps[0], ps[1]); !errors.Is(err, ErrParam) {
			t.Fatalf("NewWriter(%d,%d)=%v", ps[0], ps[1], err)
		}
	}
	if _, err := NewWriter(1, 1); err != nil {
		t.Fatal(err)
	}
}

func TestClosedWriter(t *testing.T) {
	w, _ := NewWriter(2, 2)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte{1}); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
	if err := w.Flush(); !errors.Is(err, ErrClosed) {
		t.Fatalf("flush after close: %v", err)
	}
	if err := w.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("close after close: %v", err)
	}
	if got := w.Bytes(); !bytes.Equal(got, identifierChunk) {
		t.Fatalf("output changed: % x", got)
	}
}

func TestEmptyStreamAndZeroWrite(t *testing.T) {
	w, _ := NewWriter(1, 1)
	if _, err := w.Write(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte{}); err != nil {
		t.Fatal(err)
	}
	if got := w.Bytes(); len(got) != 0 {
		t.Fatalf("identifier written early: % x", got)
	}
	mustClose(t, w)
	if got := w.Bytes(); !bytes.Equal(got, identifierChunk) {
		t.Fatalf("empty stream = % x", got)
	}

	w2, _ := NewWriter(1, 1)
	if err := w2.Flush(); err != nil {
		t.Fatal(err)
	}
	mustClose(t, w2)
	if got := w2.Bytes(); !bytes.Equal(got, identifierChunk) {
		t.Fatalf("empty flush stream = % x", got)
	}
}

func TestSize15And16Boundaries(t *testing.T) {
	w, _ := NewWriter(1, 1)
	flushBlock(t, w, bytes.Repeat([]byte{'a'}, 15))
	flushBlock(t, w, bytes.Repeat([]byte{'a'}, 16))
	mustClose(t, w)
	chunks := parseFrames(t, w.Bytes())
	if len(chunks) != 3 {
		t.Fatalf("chunks=%d", len(chunks))
	}
	if chunks[1].typ != chunkRaw || len(chunks[1].load) != 19 {
		t.Fatalf("15-byte block type=%d loadlen=%d", chunks[1].typ, len(chunks[1].load))
	}
	if chunks[2].typ != chunkRLE || !bytes.Equal(chunks[2].load[4:], []byte{0x8D, 0x61}) {
		t.Fatalf("16-byte block type=%d body=% x", chunks[2].typ, chunks[2].load[4:])
	}
}

func TestGainEqualityNotCompressed(t *testing.T) {
	// n=20：3 组 3 连段（每组 2 令牌）+ 11 字面（12 令牌），
	// RLE 长 18，恰等于 n-n/8=18，必须原样。
	data := []byte("xxxyyyzzz" + "01234567890")
	if len(data) != 20 {
		t.Fatalf("setup len=%d", len(data))
	}
	if got := rleEncode(data); len(got) != 18 {
		t.Fatalf("setup: rle len=%d (data=% x rle=% x)", len(got), data, got)
	}
	w, _ := NewWriter(1, 1)
	flushBlock(t, w, data)
	mustClose(t, w)
	chunks := parseFrames(t, w.Bytes())
	if chunks[1].typ != chunkRaw {
		t.Fatalf("equality must be raw, got type %d", chunks[1].typ)
	}
}

func TestExact65536Block(t *testing.T) {
	w, _ := NewWriter(1, 1)
	data := bytes.Repeat([]byte{'q'}, maxChunkData)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if got := w.Bytes(); len(got) != 1028 {
		t.Fatalf("full block emitted immediately: out len=%d", len(got))
	}
	chunks := parseFrames(t, w.Bytes())
	if chunks[1].typ != chunkRLE || len(chunks[1].load) != 1014 {
		t.Fatalf("full block: type=%d load=%d", chunks[1].typ, len(chunks[1].load))
	}

	w2, _ := NewWriter(1, 1)
	big := make([]byte, maxChunkData+1)
	big[maxChunkData] = 'z'
	if _, err := w2.Write(big); err != nil {
		t.Fatal(err)
	}
	if len(w2.buf) != 1 {
		t.Fatalf("residual buf=%d", len(w2.buf))
	}
	mustClose(t, w2)
	chunks2 := parseFrames(t, w2.Bytes())
	if len(chunks2) != 3 || chunks2[2].typ != chunkRaw || len(chunks2[2].load) != 5 {
		t.Fatalf("65537 split: chunks=%d lastLoad=%d lastType=%d",
			len(chunks2), len(chunks2[2].load), chunks2[2].typ)
	}
}

func TestFlushPositionsAndWriteSplits(t *testing.T) {
	data := bytes.Repeat([]byte{'a'}, 40)

	splitsEqual := func(cuts []int) []byte {
		w, _ := NewWriter(3, 4)
		off := 0
		for _, cut := range cuts {
			if _, err := w.Write(data[off:cut]); err != nil {
				t.Fatal(err)
			}
			off = cut
		}
		mustClose(t, w)
		return w.Bytes()
	}

	out1 := splitsEqual([]int{40})
	out2 := splitsEqual([]int{1, 2, 3, 4, 10, 20, 30, 40})
	out3 := splitsEqual([]int{13, 27, 40})
	if !bytes.Equal(out1, out2) || !bytes.Equal(out2, out3) {
		t.Fatal("write splits changed output")
	}

	flushVariant := func(at int) []byte {
		w, _ := NewWriter(3, 4)
		flushBlock(t, w, data[:at])
		if _, err := w.Write(data[at:]); err != nil {
			t.Fatal(err)
		}
		mustClose(t, w)
		return w.Bytes()
	}
	if bytes.Equal(flushVariant(16), flushVariant(32)) {
		t.Fatal("flush position did not change output")
	}
}

func TestSuccessResetsFailCounter(t *testing.T) {
	w, _ := NewWriter(3, 4)
	flushBlock(t, w, incompressibleBlock(100, 0))
	flushBlock(t, w, incompressibleBlock(100, 5))
	flushBlock(t, w, bytes.Repeat([]byte{'a'}, 100))
	flushBlock(t, w, incompressibleBlock(100, 9))
	flushBlock(t, w, incompressibleBlock(100, 2))
	flushBlock(t, w, bytes.Repeat([]byte{'z'}, 100))
	mustClose(t, w)
	want := []byte{chunkRaw, chunkRaw, chunkRLE, chunkRaw, chunkRaw, chunkRLE}
	if got := chunkTypes(parseFrames(t, w.Bytes())); !bytes.Equal(got, want) {
		t.Fatalf("types=% x want=% x", got, want)
	}
}

func TestSkipAndProbeCycle(t *testing.T) {
	w, _ := NewWriter(3, 4)
	inc := func(seed byte) []byte { return incompressibleBlock(100, seed) }
	aa := bytes.Repeat([]byte{'a'}, 100)

	flushBlock(t, w, inc(0))
	flushBlock(t, w, inc(1))
	flushBlock(t, w, inc(2))
	if w.skip != 4 || w.fail != 0 {
		t.Fatalf("after 3 fails: skip=%d fail=%d", w.skip, w.fail)
	}
	flushBlock(t, w, aa)
	flushBlock(t, w, aa)
	flushBlock(t, w, aa)
	flushBlock(t, w, aa)
	if w.skip != 0 || !w.probe {
		t.Fatalf("after 4 skipped: skip=%d probe=%v", w.skip, w.probe)
	}
	flushBlock(t, w, inc(3))
	if w.curS != 8 || w.skip != 8 || w.probe {
		t.Fatalf("probe fail: curS=%d skip=%d probe=%v", w.curS, w.skip, w.probe)
	}
	for i := 0; i < 8; i++ {
		flushBlock(t, w, aa)
	}
	if w.skip != 0 || !w.probe {
		t.Fatalf("after second skip run: skip=%d probe=%v", w.skip, w.probe)
	}
	flushBlock(t, w, aa)
	if w.curS != 4 || w.probe {
		t.Fatalf("probe success should restore curS=S: curS=%d probe=%v", w.curS, w.probe)
	}
	flushBlock(t, w, inc(4))
	flushBlock(t, w, inc(5))
	flushBlock(t, w, inc(6))
	if w.skip != w.curS || w.curS != 4 {
		t.Fatalf("failures after restore use S: skip=%d curS=%d", w.skip, w.curS)
	}
	mustClose(t, w)

	want := []byte{
		chunkRaw, chunkRaw, chunkRaw,
		chunkRaw, chunkRaw, chunkRaw, chunkRaw,
		chunkRaw,
		chunkRaw, chunkRaw, chunkRaw, chunkRaw, chunkRaw, chunkRaw, chunkRaw, chunkRaw,
		chunkRLE,
		chunkRaw, chunkRaw, chunkRaw,
	}
	if got := chunkTypes(parseFrames(t, w.Bytes())); !bytes.Equal(got, want) {
		t.Fatalf("types=% x\nwant =% x", got, want)
	}
}

func TestSmallBlocksDuringSkipAndProbe(t *testing.T) {
	w, _ := NewWriter(3, 4)
	flushBlock(t, w, incompressibleBlock(100, 0))
	flushBlock(t, w, incompressibleBlock(100, 1))
	flushBlock(t, w, incompressibleBlock(100, 2))
	if w.skip != 4 {
		t.Fatalf("skip=%d", w.skip)
	}
	flushBlock(t, w, []byte("abc"))
	if w.skip != 3 || w.probe {
		t.Fatalf("small block consumed skip: skip=%d", w.skip)
	}
	flushBlock(t, w, bytes.Repeat([]byte{'a'}, 100))
	if w.skip != 2 {
		t.Fatalf("compressible block still raw in skip: skip=%d", w.skip)
	}
	flushBlock(t, w, []byte("def"))
	flushBlock(t, w, bytes.Repeat([]byte{'b'}, 100))
	if w.skip != 0 || !w.probe {
		t.Fatalf("probe not set: skip=%d probe=%v", w.skip, w.probe)
	}
	flushBlock(t, w, []byte("gh"))
	if !w.probe || w.fail != 0 || w.skip != 0 {
		t.Fatalf("small block moved probe state: probe=%v fail=%d skip=%d", w.probe, w.fail, w.skip)
	}
	flushBlock(t, w, incompressibleBlock(100, 4))
	if w.curS != 8 || w.skip != 8 {
		t.Fatalf("probe fail doubling: curS=%d skip=%d", w.curS, w.skip)
	}
	mustClose(t, w)
	for _, c := range parseFrames(t, w.Bytes()) {
		if c.typ != identifierType && c.typ != chunkRaw {
			t.Fatal("unexpected compressed chunk in skip/probe phase")
		}
	}
}

func TestProbeFailDoublingCap(t *testing.T) {
	// F=1,S=1：常规失败立即 skip=curS；skip 尾块兼探测，探测失败时
	// curS 走 1->2->4->8->8（封顶 8S）。
	w, _ := NewWriter(1, 1)
	inc := func(seed byte) []byte { return incompressibleBlock(100, seed) }

	flushBlock(t, w, inc(0)) // 常规失败 f==F -> skip=curS=1
	if w.skip != 1 || w.curS != 1 {
		t.Fatalf("initial: skip=%d curS=%d", w.skip, w.curS)
	}
	// skip 尾块（第 2 块）置 probe；同一小块不可压 => 探测失败，curS 翻倍
	probeFail := func(seed byte, wantCurS int) {
		t.Helper()
		n := w.skip
		for i := 0; i < n; i++ {
			flushBlock(t, w, inc(seed+byte(i)))
		}
		if w.skip != 0 || !w.probe {
			t.Fatalf("before probe fail: skip=%d probe=%v", w.skip, w.probe)
		}
		flushBlock(t, w, inc(seed+99)) // 探测块：尝试失败，curS 翻倍
		if w.curS != wantCurS || w.skip != wantCurS {
			t.Fatalf("doubling: curS=%d skip=%d want=%d", w.curS, w.skip, wantCurS)
		}
	}
	probeFail(10, 2)
	probeFail(30, 4)
	probeFail(60, 8)
	probeFail(90, 8) // 封顶 8S

	// 探测成功令 curS 回到 S
	n := w.skip
	for i := 0; i < n; i++ {
		flushBlock(t, w, inc(byte(120+i)))
	}
	flushBlock(t, w, bytes.Repeat([]byte{'z'}, 100)) // 探测成功
	if w.curS != 1 || w.probe || w.skip != 0 {
		t.Fatalf("probe success restore: curS=%d skip=%d probe=%v", w.curS, w.skip, w.probe)
	}
	mustClose(t, w)
}
