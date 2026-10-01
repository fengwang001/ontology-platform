package fragmentlog

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

func TestNewWriterBlockSize(t *testing.T) {
	for _, b := range []int{0, 1, 6, 7, -5} {
		if _, err := NewWriter(&bytes.Buffer{}, b); !errors.Is(err, ErrBlockSizeTooSmall) {
			t.Fatalf("blockSize %d: want ErrBlockSizeTooSmall, got %v", b, err)
		}
		t.Logf("blockSize=%d -> 拒绝: %v (判定: 不大于头长 7)", b, ErrBlockSizeTooSmall)
	}
	for _, b := range []int{65536, 65537, 1 << 20} {
		if _, err := NewWriter(&bytes.Buffer{}, b); !errors.Is(err, ErrBlockSizeTooLarge) {
			t.Fatalf("blockSize %d: want ErrBlockSizeTooLarge, got %v", b, err)
		}
		t.Logf("blockSize=%d -> 拒绝: %v (判定: 超过 65535)", b, ErrBlockSizeTooLarge)
	}
	for _, b := range []int{8, 9, 32, 65535} {
		if _, err := NewWriter(&bytes.Buffer{}, b); err != nil {
			t.Fatalf("blockSize %d: unexpected error %v", b, err)
		}
	}
	if _, err := NewReader(bytes.NewReader(nil), 7); !errors.Is(err, ErrBlockSizeTooSmall) {
		t.Fatalf("reader blockSize 7: want ErrBlockSizeTooSmall")
	}
	if _, err := NewReader(bytes.NewReader(nil), 65536); !errors.Is(err, ErrBlockSizeTooLarge) {
		t.Fatalf("reader blockSize 65536: want ErrBlockSizeTooLarge")
	}
}

func TestAppendRecordTooLarge(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 32)
	if err != nil {
		t.Fatal(err)
	}
	off0, err := w.Append([]byte("anchor"))
	if err != nil {
		t.Fatal(err)
	}
	sizeBefore := buf.Len()
	offBefore := w.Offset()

	if _, err := w.Append(make([]byte, MaxRecord+1)); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("oversized record: want ErrRecordTooLarge, got %v", err)
	}
	if buf.Len() != sizeBefore || w.Offset() != offBefore {
		t.Fatalf("rejected append changed state: size %d->%d, offset %d->%d",
			sizeBefore, buf.Len(), offBefore, w.Offset())
	}
	t.Logf("输入: 长度 %d (> MaxRecord %d) -> 拒绝: %v; 已写字节 %d 与偏移 %d 均未变",
		MaxRecord+1, MaxRecord, ErrRecordTooLarge, buf.Len(), w.Offset())

	off1, err := w.Append([]byte("next"))
	if err != nil {
		t.Fatal(err)
	}
	if off1 != off0+7+6 {
		t.Fatalf("offset after rejected append = %d, want %d", off1, off0+7+6)
	}
}

// TestRemCases exercises leftover-space sizes 0..8 at append time:
// rem 0 means the previous record filled its block exactly; rem 1..6 are
// zero-padded; rem 7 yields a zero-length first fragment; rem 8 yields a
// one-byte first fragment.
func TestRemCases(t *testing.T) {
	const B = 32
	for rem := 0; rem <= 8; rem++ {
		t.Run("", func(t *testing.T) {
			var buf []byte
			// filler occupies one fragment so that off%B == (B-rem)%B.
			fillerLen := (B - rem + B) % B
			if fillerLen == 0 {
				fillerLen = B - HeaderSize // exactly fill the block
			} else {
				fillerLen -= HeaderSize
			}
			filler := bytes.Repeat([]byte{0xAA}, fillerLen)
			var off int64
			buf, off = naiveAppend(B, buf, filler)
			if off != 0 {
				t.Fatalf("filler offset = %d, want 0", off)
			}

			rec := []byte("0123456789") // 10 bytes
			want, wantOff := naiveAppend(B, buf, rec)

			var got bytes.Buffer
			w, err := NewWriter(&got, B)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Append(filler); err != nil {
				t.Fatal(err)
			}
			gotOff, err := w.Append(rec)
			if err != nil {
				t.Fatal(err)
			}
			if gotOff != wantOff {
				t.Fatalf("rem=%d: offset = %d, want %d", rem, gotOff, wantOff)
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("rem=%d: bytes differ\n got: %x\nwant: %x", rem, got.Bytes(), want)
			}

			// Read both records back.
			rd, err := NewReader(bytes.NewReader(got.Bytes()), B)
			if err != nil {
				t.Fatal(err)
			}
			r1, o1, err := rd.Next()
			if err != nil || !bytes.Equal(r1, filler) || o1 != 0 {
				t.Fatalf("rem=%d: first record = (%x, %d, %v)", rem, r1, o1, err)
			}
			r2, o2, err := rd.Next()
			if err != nil || !bytes.Equal(r2, rec) || o2 != wantOff {
				t.Fatalf("rem=%d: second record = (%x, %d, %v)", rem, r2, o2, err)
			}
			t.Logf("rem=%d: 填充 %d 字节后余 %d, 记录 10 字节 -> 首片头偏移 %d, 输出 %d 字节, 读回一致",
				rem, fillerLen, (B-len(filler)-HeaderSize+B)%B, wantOff, len(want))
		})
	}
}

func TestEmptyRecord(t *testing.T) {
	const B = 16
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, B)
	off, err := w.Append(nil)
	if err != nil {
		t.Fatal(err)
	}
	if off != 0 || buf.Len() != HeaderSize {
		t.Fatalf("empty record: off=%d len=%d, want 0 and %d", off, buf.Len(), HeaderSize)
	}
	if buf.Bytes()[6] != TypeFull {
		t.Fatalf("empty record fragment type = %d, want TypeFull", buf.Bytes()[6])
	}
	rd, _ := NewReader(bytes.NewReader(buf.Bytes()), B)
	rec, roff, err := rd.Next()
	if err != nil {
		t.Fatal(err)
	}
	if len(rec) != 0 || roff != 0 {
		t.Fatalf("read empty record = (%v, %d)", rec, roff)
	}
	t.Logf("空记录 -> 零长完整片段, 偏移 %d, 输出 %x, 读回 len=%d", off, buf.Bytes(), len(rec))
}

func TestRecordFillsBlockExactly(t *testing.T) {
	const B = 32
	rec := bytes.Repeat([]byte{0x5A}, B-HeaderSize)
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, B)
	off, err := w.Append(rec)
	if err != nil {
		t.Fatal(err)
	}
	if off != 0 || buf.Len() != B {
		t.Fatalf("exact-fill: off=%d len=%d, want 0 and %d", off, buf.Len(), B)
	}
	// Next record must start at the next block boundary with no padding.
	off2, err := w.Append([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if off2 != B {
		t.Fatalf("record after exact-fill starts at %d, want %d", off2, B)
	}
	t.Logf("记录恰好填满块 (%d 数据 + %d 头 = %d), 下一条起始于块边界 %d",
		len(rec), HeaderSize, B, off2)
}

func TestRecordSpansThreePlusBlocks(t *testing.T) {
	const B = 32
	rec := make([]byte, 100) // 4 fragments of 25 bytes each
	for i := range rec {
		rec[i] = byte(i)
	}
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, B)
	off, err := w.Append(rec)
	if err != nil {
		t.Fatal(err)
	}
	if off != 0 {
		t.Fatalf("offset = %d, want 0", off)
	}
	wantTypes := []byte{TypeFirst, TypeMiddle, TypeMiddle, TypeLast}
	for i, wt := range wantTypes {
		fragOff := i * B
		if got := buf.Bytes()[fragOff+6]; got != wt {
			t.Fatalf("fragment %d type = %d, want %d", i, got, wt)
		}
		if n := int(buf.Bytes()[fragOff+4]) | int(buf.Bytes()[fragOff+5])<<8; n != 25 {
			t.Fatalf("fragment %d length = %d, want 25", i, n)
		}
	}
	rd, _ := NewReader(bytes.NewReader(buf.Bytes()), B)
	got, goff, err := rd.Next()
	if err != nil || !bytes.Equal(got, rec) || goff != 0 {
		t.Fatalf("read back = (len %d, %d, %v)", len(got), goff, err)
	}
	t.Logf("100 字节记录跨 4 块 (B=%d): 类型序列 %v, 读回一致", B, wantTypes)
}

// TestRandomRoundTripVsNaive cross-checks bytes and offsets against the
// naive writer, and the read-back event stream against the naive reader.
func TestRandomRoundTripVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, B := range []int{8, 9, 16, 32, 100, 65535} {
		var want []byte
		var got bytes.Buffer
		w, _ := NewWriter(&got, B)
		var records [][]byte
		n := 60
		if B == 65535 {
			n = 8
		}
		for i := 0; i < n; i++ {
			l := rng.Intn(4 * B)
			if B == 65535 {
				l = rng.Intn(200000)
			}
			rec := make([]byte, l)
			rng.Read(rec)
			records = append(records, rec)
			var wo int64
			want, wo = naiveAppend(B, want, rec)
			go_, err := w.Append(rec)
			if err != nil {
				t.Fatal(err)
			}
			if go_ != wo {
				t.Fatalf("B=%d rec %d: offset %d, want %d", B, i, go_, wo)
			}
		}
		if !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("B=%d: writer bytes differ from naive writer", B)
		}
		rd, _ := NewReader(bytes.NewReader(got.Bytes()), B)
		gotEvents := realReadAll(t, rd)
		wantEvents := naiveReadAll(B, want)
		if len(gotEvents) != len(wantEvents) {
			t.Fatalf("B=%d: %d events, want %d", B, len(gotEvents), len(wantEvents))
		}
		for i := range gotEvents {
			if !eventsEqual(gotEvents[i], wantEvents[i]) {
				t.Fatalf("B=%d event %d: got %v, want %v", B, i, gotEvents[i], wantEvents[i])
			}
		}
		// The record sequence read back equals the written sequence.
		ri := 0
		for _, ev := range gotEvents {
			if ev.kind != "record" {
				continue
			}
			if !bytes.Equal(ev.rec, records[ri]) {
				t.Fatalf("B=%d: record %d mismatch", B, ri)
			}
			ri++
		}
		if ri != len(records) {
			t.Fatalf("B=%d: read %d records, wrote %d", B, ri, len(records))
		}
		t.Logf("B=%d: %d 条随机记录, 写出 %d 字节与朴素实现逐字节一致, 读回序列一致",
			B, n, len(want))
	}
}
