// wal 与 recover 的合并表驱动测试（按包合并为一个测试文件）。
package wal_test

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"ontology/recover"
	"ontology/wal"
)

type spec struct {
	base     uint64
	payloads [][]byte
}

func buildLog(batches []spec) []byte {
	data := append([]byte(nil), wal.FileHeader...)
	for _, b := range batches {
		data = append(data, wal.EncodeBatch(b.base, b.payloads)...)
	}
	return data
}

func TestRoundTrip(t *testing.T) {
	rows := []struct {
		name    string
		batches []spec
	}{
		{"single-empty", []spec{{1, [][]byte{nil}}}},
		{"single-one", []spec{{1, [][]byte{[]byte("hello")}}}},
		{"many", []spec{
			{1, [][]byte{[]byte("a"), nil, []byte("cc")}},
			{4, [][]byte{[]byte("dddd")}},
			{5, [][]byte{[]byte("e"), []byte("ff"), []byte("ggg"), []byte("hhhh")}},
		}},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			got := recover.Parse(buildLog(tc.batches))
			if got.Err != nil || len(got.Batches) != len(tc.batches) {
				t.Fatalf("err=%v batches=%d", got.Err, len(got.Batches))
			}
			for i, want := range tc.batches {
				g := got.Batches[i]
				if g.BaseSeq != want.base || len(g.Payloads) != len(want.payloads) {
					t.Fatalf("batch %d header mismatch", i)
				}
				for j, p := range want.payloads {
					if !bytes.Equal(g.Payloads[j], p) {
						t.Fatalf("batch %d entry %d = %q want %q", i, j, g.Payloads[j], p)
					}
				}
			}
			if got.LastSeq != tc.batches[len(tc.batches)-1].
				base+uint64(len(tc.batches[len(tc.batches)-1].payloads))-1 {
				t.Fatal("last seq wrong")
			}
		})
	}
}

func mkBatches(n, entries int) []spec {
	out := make([]spec, n)
	seq := uint64(1)
	for i := range out {
		ps := make([][]byte, entries)
		for j := range ps {
			ps[j] = []byte(fmt.Sprintf("b%de%d", i, j))
		}
		out[i] = spec{seq, ps}
		seq += uint64(entries)
	}
	return out
}

func classify(err error) string {
	switch {
	case errors.Is(err, recover.ErrHeaderIncomplete):
		return "header"
	case errors.Is(err, recover.ErrBatchHeaderIncomplete):
		return "batch-header"
	case errors.Is(err, recover.ErrEntryIncomplete):
		return "entry"
	case errors.Is(err, recover.ErrCRCMismatch):
		return "crc"
	default:
		return "clean"
	}
}

func TestEveryTruncationPoint(t *testing.T) {
	batches := mkBatches(3, 5)
	data := buildLog(batches)
	ends := []int{len(wal.FileHeader)}
	for _, b := range batches {
		ends = append(ends, ends[len(ends)-1]+wal.BatchSize(b.payloads))
	}
	ranges := map[string][2]int{}
	for cut := 1; cut < len(data); cut++ {
		r := recover.Parse(data[:cut])
		kind := classify(r.Err)
		prev, ok := ranges[kind]
		if !ok {
			prev = [2]int{cut, cut}
		}
		if cut < prev[0] {
			prev[0] = cut
		}
		if cut > prev[1] {
			prev[1] = cut
		}
		ranges[kind] = prev
		wantN := 0
		for _, e := range ends[1:] {
			if cut >= e {
				wantN++
			}
		}
		if len(r.Batches) != wantN {
			t.Fatalf("cut=%d visible=%d want %d", cut, len(r.Batches), wantN)
		}
		if r.Err != nil && len(r.Batches) > 0 {
			last := r.Batches[len(r.Batches)-1]
			if last.EndSeq() != r.LastSeq {
				t.Fatalf("cut=%d lastseq broken", cut)
			}
		}
	}
	for _, kind := range []string{"header", "batch-header", "entry", "crc"} {
		if _, ok := ranges[kind]; !ok {
			t.Fatalf("missing truncation class %s, got %v", kind, ranges)
		}
	}
	t.Logf("ranges: %v total=%d", ranges, len(data))
}

func TestCRCBatchAtomicity(t *testing.T) {
	batches := mkBatches(3, 5)
	data := buildLog(batches)
	off := len(wal.FileHeader)
	off += wal.BatchSize(batches[0].payloads) // 批 2 起点
	off += 16 + 4 + len(batches[1].payloads[0]) + 4 + len(batches[1].payloads[1])
	data[off] ^= 0xFF // 改坏批 2 第 3 条首字节
	r := recover.Parse(data)
	if !errors.Is(r.Err, recover.ErrCRCMismatch) {
		t.Fatalf("err=%v want crc mismatch", r.Err)
	}
	if len(r.Batches) != 2 || r.Batches[0].BaseSeq != 1 || r.Batches[1].BaseSeq != 11 {
		t.Fatalf("bad batch must drop all 5 entries, neighbors survive, got %d batches", len(r.Batches))
	}
}

func TestCrashMidWrite(t *testing.T) {
	batches := mkBatches(2, 5)
	data := buildLog(batches)
	cut := len(data) - (wal.BatchSize(batches[1].payloads) / 2)
	r := recover.Parse(data[:cut])
	if len(r.Batches) != 1 || r.Batches[0].BaseSeq != 1 || r.LastSeq != 5 {
		t.Fatalf("partial batch invisible, got %d batches lastseq=%d", len(r.Batches), r.LastSeq)
	}
	if r.Err == nil {
		t.Fatal("partial tail must be classified")
	}
}
