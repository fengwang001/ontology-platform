package recordlog

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
)

// bytesPattern builds a deterministic record of n bytes.
func bytesPattern(seed, n int) []byte {
	b := make([]byte, n)
	x := uint32(seed*2654435761 + 1)
	for i := range b {
		x = x*1103515245 + 12345
		b[i] = byte(x>>16) ^ byte(i)
	}
	return b
}

// drainReader consumes a Reader and returns every outcome, including
// the terminal io.EOF.
func drainReader(t *testing.T, r *Reader) []naiveReadResult {
	t.Helper()
	var out []naiveReadResult
	for {
		rec, off, err := r.Next()
		out = append(out, naiveReadResult{record: rec, offset: off, err: err})
		if err == io.EOF {
			return out
		}
		if errors.Is(err, ErrTruncated) {
			rec2, off2, nextErr := r.Next()
			if rec2 != nil || off2 != 0 || nextErr != io.EOF {
				t.Fatalf("after truncation expected io.EOF, got %v", nextErr)
			}
			out = append(out, naiveReadResult{err: nextErr})
			return out
		}
	}
}

func sameResults(a, b []naiveReadResult) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i].record, b[i].record) || a[i].offset != b[i].offset {
			return false
		}
		if !sameErr(a[i].err, b[i].err) {
			return false
		}
	}
	return true
}

func sameErr(a, b error) bool {
	if a == b {
		return true
	}
	ca, oka := a.(*CorruptError)
	cb, okb := b.(*CorruptError)
	if oka != okb {
		return false
	}
	if !oka {
		return a == b
	}
	return ca.Offset == cb.Offset && errors.Is(ca, cb.Err)
}

func TestInvalidBlockSize(t *testing.T) {
	for _, b := range []int{0, 1, 7, -1, 65536, 1 << 20} {
		if _, err := NewWriter(io.Discard, b); !errors.Is(err, ErrInvalidBlockSize) {
			t.Fatalf("NewWriter(block=%d) err=%v, want ErrInvalidBlockSize", b, err)
		}
		if _, err := NewReader(bytes.NewReader(nil), b); !errors.Is(err, ErrInvalidBlockSize) {
			t.Fatalf("NewReader(block=%d) err=%v, want ErrInvalidBlockSize", b, err)
		}
	}
	if _, err := NewWriter(io.Discard, 8); err != nil {
		t.Fatalf("valid block size rejected: %v", err)
	}
}

func TestRejectRecordTooLarge(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(bytesPattern(1, 10)); err != nil {
		t.Fatal(err)
	}
	before := buf.Len()
	if _, err := w.Append(make([]byte, MaxRecord+1)); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("err=%v, want ErrRecordTooLarge", err)
	}
	if buf.Len() != before {
		t.Fatalf("rejected Append changed bytes: before=%d after=%d", before, buf.Len())
	}
	if got := w.Offset(); got != int64(before) {
		t.Fatalf("rejected Append changed offset: %d", got)
	}
	if _, err := w.Append(make([]byte, MaxRecord)); err != nil {
		t.Fatalf("MaxRecord record rejected: %v", err)
	}
}

// TestRemaining0to8 reaches rem = 0..8 immediately before the target
// Append using one preceding full record:
// rem 0..6 via fillLen B-14+rem, rem 7..8 via fillLen B-7-rem.
func TestRemaining0to8(t *testing.T) {
	const B = 16
	type scenario struct {
		rem      int
		fillLen  int
		record   []byte
		firstTyp byte
		why      string
	}
	scenarios := []scenario{
		{0, B - 7, bytesPattern(0, 5), TypeFull, "rem=0: at block start"},
		{1, B - 8, bytesPattern(1, 5), TypeFull, "rem=1: pad 1 zero"},
		{2, B - 9, bytesPattern(2, 5), TypeFull, "rem=2: pad 2 zeros"},
		{3, B - 10, bytesPattern(3, 5), TypeFull, "rem=3: pad 3 zeros"},
		{4, B - 11, bytesPattern(4, 5), TypeFull, "rem=4: pad 4 zeros"},
		{5, B - 12, bytesPattern(5, 5), TypeFull, "rem=5: pad 5 zeros"},
		{6, B - 13, bytesPattern(6, 5), TypeFull, "rem=6: pad 6 zeros"},
		{7, B - 14, bytesPattern(7, 20), TypeFirst, "rem=7: zero-length first"},
		{8, B - 15, bytesPattern(8, 20), TypeFirst, "rem=8: one-byte first"},
	}

	for _, sc := range scenarios {
		t.Run(sc.why, func(t *testing.T) {
			fill := bytesPattern(50+sc.rem, sc.fillLen)

			var buf bytes.Buffer
			w, _ := NewWriter(&buf, B)
			if _, err := w.Append(fill); err != nil {
				t.Fatal(err)
			}
			used := int(w.Offset() % B)
			gotRem := 0
			if used != 0 {
				gotRem = B - used
			}
			if gotRem != sc.rem {
				t.Fatalf("setup rem=%d want %d", gotRem, sc.rem)
			}

			off, err := w.Append(sc.record)
			if err != nil {
				t.Fatal(err)
			}
			// rem<7 pads to the next block; rem>=7 starts in place.
			wantOff := int64(B - sc.rem)
			if sc.rem < 7 {
				wantOff = int64(B)
				if off%int64(B) != 0 {
					t.Fatalf("first fragment offset %d not at block start", off)
				}
			}
			if off != wantOff {
				t.Fatalf("first fragment offset=%d want %d", off, wantOff)
			}
			if got := buf.Bytes()[off+6]; got != sc.firstTyp {
				t.Fatalf("first type=%d want %d", got, sc.firstTyp)
			}

			nb := newNaiveWriter(B)
			nb.appendRecord(fill)
			expectOff := nb.appendRecord(sc.record)
			if expectOff != off {
				t.Fatalf("offset got=%d naive=%d", off, expectOff)
			}
			if !bytes.Equal(buf.Bytes(), nb.buf) {
				t.Fatalf("bytes differ from naive\n got: % x\nwant: % x", buf.Bytes(), nb.buf)
			}

			r, _ := NewReader(bytes.NewReader(buf.Bytes()), B)
			got := drainReader(t, r)
			want := drainNaiveReader(nb.buf, B)
			t.Logf("输入: rem=%d fillLen=%d recordLen=%d；输出: % x；判定依据: %s",
				sc.rem, sc.fillLen, len(sc.record), buf.Bytes(), sc.why)
			if !sameResults(got, want) {
				t.Fatalf("read differs from naive\n got:\n%s\nwant:\n%s",
					formatResults(got), formatResults(want))
			}
		})
	}
}

// TestSpansThreeBlocks writes a record spanning at least three blocks.
func TestSpansThreeBlocks(t *testing.T) {
	const B = 32
	fill := bytesPattern(1, 3) // 10 bytes used
	record := bytesPattern(2, 100)

	var buf bytes.Buffer
	w, _ := NewWriter(&buf, B)
	if _, err := w.Append(fill); err != nil {
		t.Fatal(err)
	}
	off, err := w.Append(record)
	if err != nil {
		t.Fatal(err)
	}

	var types []byte
	pos := int(off)
	seen := 0
	for seen < len(record) {
		rem := B - pos%B
		if rem < 7 {
			if !bytes.Equal(buf.Bytes()[pos:pos+rem], make([]byte, rem)) {
				t.Fatalf("padding not zeros at %d", pos)
			}
			pos += rem
			continue
		}
		h := buf.Bytes()[pos : pos+7]
		l := int(h[4]) | int(h[5])<<8
		types = append(types, h[6])
		seen += l
		pos += 7 + l
	}
	if types[0] != TypeFirst || types[len(types)-1] != TypeLast {
		t.Fatalf("types=%v, want first...last", types)
	}
	for _, m := range types[1 : len(types)-1] {
		if m != TypeMiddle {
			t.Fatalf("types=%v, inner must be middle", types)
		}
	}
	if len(types) < 3 {
		t.Fatalf("record spanned only %d fragments, want >= 3", len(types))
	}

	nb := newNaiveWriter(B)
	nb.appendRecord(fill)
	nb.appendRecord(record)
	if !bytes.Equal(buf.Bytes(), nb.buf) {
		t.Fatalf("bytes differ from naive\n got: % x\nwant: % x", buf.Bytes(), nb.buf)
	}

	r, _ := NewReader(bytes.NewReader(buf.Bytes()), B)
	got := drainReader(t, r)
	want := drainNaiveReader(nb.buf, B)
	t.Logf("输入: 100字节跨块记录, B=%d；输出片段类型序列=%d；判定依据: first/middle*/last", B, types)
	if !sameResults(got, want) {
		t.Fatalf("read mismatch\n got:\n%s\nwant:\n%s", formatResults(got), formatResults(want))
	}
}

func TestEmptyRecords(t *testing.T) {
	const B = 32
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, B)
	off1, err := w.Append(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := buf.Bytes()[off1+6]; got != TypeFull {
		t.Fatalf("empty record type=%d want full", got)
	}
	if l := int(buf.Bytes()[off1+4]) | int(buf.Bytes()[off1+5])<<8; l != 0 {
		t.Fatalf("empty record length=%d", l)
	}
	for i := 0; i < 6; i++ {
		if _, err := w.Append(nil); err != nil {
			t.Fatal(err)
		}
	}

	nb := newNaiveWriter(B)
	for i := 0; i < 7; i++ {
		nb.appendRecord(nil)
	}
	if !bytes.Equal(buf.Bytes(), nb.buf) {
		t.Fatalf("empty layout differs:\n got: % x\nwant: % x", buf.Bytes(), nb.buf)
	}
	r, _ := NewReader(bytes.NewReader(buf.Bytes()), B)
	res := drainReader(t, r)
	if len(res) != 8 {
		t.Fatalf("got %d outcomes, want 8 (7 records + EOF)", len(res))
	}
	for i := 0; i < 7; i++ {
		if len(res[i].record) != 0 || res[i].err != nil {
			t.Fatalf("empty record %d: %+v", i, res[i])
		}
	}
	t.Logf("输入: 7 条空记录；输出: 7 个零长 full + 块填充；判定依据: rem<7 补零另起新块")
}

func TestRecordFillsBlock(t *testing.T) {
	const B = 32
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, B)
	rec := bytesPattern(3, B-7)
	off, err := w.Append(rec)
	if err != nil {
		t.Fatal(err)
	}
	if off != 0 || buf.Len() != B {
		t.Fatalf("off=%d len=%d, want 0 and %d", off, buf.Len(), B)
	}
	off2, err := w.Append(rec)
	if err != nil {
		t.Fatal(err)
	}
	if off2 != B {
		t.Fatalf("second record off=%d, want %d", off2, B)
	}
	t.Logf("输入: 2 条恰好填满块的记录；输出长度=%d；判定依据: rem=0 直接起新块", buf.Len())
}

type syncWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// TestConcurrentAppend runs many concurrent appends and checks that
// the resulting stream round-trips to exactly the set of written
// records in some serial order with matching offsets.
func TestConcurrentAppend(t *testing.T) {
	const B = 64
	var lockedBuf bytes.Buffer
	var mu sync.Mutex
	w, _ := NewWriter(&syncWriter{w: &lockedBuf, mu: &mu}, B)

	const goroutines = 8
	const each = 50
	type result struct {
		off int64
		rec []byte
	}
	resultsCh := make(chan result, goroutines*each)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; i < each; i++ {
				rec := bytesPattern(g*1000+i, 1+((g*7+i*13)%40))
				off, err := w.Append(rec)
				if err != nil {
					t.Errorf("append: %v", err)
					return
				}
				resultsCh <- result{off: off, rec: rec}
			}
		}(g)
	}
	close(start)
	wg.Wait()
	close(resultsCh)

	byOffset := map[int64][]byte{}
	for res := range resultsCh {
		if _, dup := byOffset[res.off]; dup {
			t.Fatalf("duplicate offset %d", res.off)
		}
		byOffset[res.off] = res.rec
	}

	// Every byte of the stream must be reachable as a valid sequence.
	r, _ := NewReader(bytes.NewReader(lockedBuf.Bytes()), B)
	got := drainReader(t, r)
	if got[len(got)-1].err != io.EOF {
		t.Fatalf("stream did not end in EOF: %v", got[len(got)-1].err)
	}
	count := 0
	for _, res := range got[:len(got)-1] {
		if res.err != nil {
			t.Fatalf("concurrent stream corrupt: %v", res.err)
		}
		want, ok := byOffset[res.offset]
		if !ok {
			t.Fatalf("unexpected offset %d", res.offset)
		}
		if !bytes.Equal(want, res.record) {
			t.Fatalf("record at %d mismatch", res.offset)
		}
		count++
	}
	if count != goroutines*each {
		t.Fatalf("read %d records, want %d", count, goroutines*each)
	}
	t.Logf("输入: %d goroutine x %d 并发追加；输出: 无损坏流，%d 条记录；判定依据: 互斥串行化等价",
		goroutines, each, count)
}

// TestConcurrentReader exercises concurrent Next calls; outcomes must
// collectively match the naive sequential scan.
func TestConcurrentReader(t *testing.T) {
	const B = 24
	nb := newNaiveWriter(B)
	var records [][]byte
	for i := 0; i < 40; i++ {
		rec := bytesPattern(i, i%37)
		records = append(records, rec)
		nb.appendRecord(rec)
	}
	data := nb.buf

	// Sequential ground truth.
	want := drainNaiveReader(data, B)
	wantRecords := map[int64][]byte{}
	for _, res := range want {
		if res.err == nil {
			wantRecords[res.offset] = res.record
		}
	}

	r, _ := NewReader(bytes.NewReader(data), B)
	var wg sync.WaitGroup
	var gotMu sync.Mutex
	gotRecords := map[int64]int{}
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				rec, off, err := r.Next()
				if err != nil {
					if err != io.EOF && !errors.Is(err, ErrTruncated) {
						t.Errorf("unexpected err: %v", err)
					}
					return
				}
				if !bytes.Equal(wantRecords[off], rec) {
					t.Errorf("offset %d mismatch", off)
				}
				gotMu.Lock()
				gotRecords[off]++
				gotMu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(gotRecords) != len(wantRecords) {
		t.Fatalf("got %d distinct records, want %d", len(gotRecords), len(wantRecords))
	}
	for off, n := range gotRecords {
		if n != 1 {
			t.Fatalf("record at %d delivered %d times", off, n)
		}
	}
	t.Logf("输入: 6 个 goroutine 并发读 %d 条记录；输出: 每条恰好一次 + EOF；判定依据: 读互斥顺序消费",
		len(wantRecords))
}

// TestMinBlockSize8 exercises the smallest legal block size: each
// non-empty fragment carries at most one data byte.
func TestMinBlockSize8(t *testing.T) {
	const B = 8
	var buf bytes.Buffer
	w, err := NewWriter(&buf, B)
	if err != nil {
		t.Fatal(err)
	}
	rec := []byte("abc")
	off0, _ := w.Append(nil) // zero-length full (7 bytes), rem=1
	off1, _ := w.Append(rec) // rem=1 => pad 1 zero; first at block 1, zero-length first; then 1-byte middle/middle/last
	if off0 != 0 {
		t.Fatalf("empty off=%d", off0)
	}
	if off1 != B {
		t.Fatalf("record off=%d want %d", off1, B)
	}
	// Walk fragments from off1: first(0), middle(1), middle(1), last(1).
	wantTypes := []byte{TypeFirst, TypeMiddle, TypeLast}
	pos := int(off1)
	for i, want := range wantTypes {
		if got := buf.Bytes()[pos+6]; got != want {
			t.Fatalf("frag %d type=%d want %d", i, got, want)
		}
		l := int(buf.Bytes()[pos+4])
		pos += 7 + l
	}
	r, _ := NewReader(bytes.NewReader(buf.Bytes()), B)
	res := drainReader(t, r)
	if len(res) != 3 || len(res[0].record) != 0 || !bytes.Equal(res[1].record, rec) {
		t.Fatalf("results=%v", res)
	}
	t.Logf("输入: B=8 空记录+3字节记录；输出: 零长 full, 补零, 零长 first, 3 个单字节片段；判定依据: 最小块 rem=7 零长首片")
}
