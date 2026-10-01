package recordlog

import (
	"bytes"
	"io"
	"testing"
)

// TestDeterministicReproduction verifies byte-identical output and
// identical offsets for the same append sequence across runs.
func TestDeterministicReproduction(t *testing.T) {
	const B = 37
	makeRecords := func() [][]byte {
		lens := []int{0, 1, 5, 30, 31, 100, 0, 65536, 7, 40, 37, 0}
		out := make([][]byte, len(lens))
		for i, n := range lens {
			out[i] = bytesPattern(900+i, n)
		}
		return out
	}
	run := func() ([]byte, []int64) {
		var buf bytes.Buffer
		w, _ := NewWriter(&buf, B)
		var offs []int64
		for _, rec := range makeRecords() {
			off, err := w.Append(rec)
			if err != nil {
				t.Fatal(err)
			}
			offs = append(offs, off)
		}
		return buf.Bytes(), offs
	}

	first, offs1 := run()
	second, offs2 := run()
	if !bytes.Equal(first, second) {
		t.Fatalf("output bytes differ across runs")
	}
	for i := range offs1 {
		if offs1[i] != offs2[i] {
			t.Fatalf("offset %d differs: %d vs %d", i, offs1[i], offs2[i])
		}
	}

	// Read-back sequence must equal written sequence, including empties.
	r, _ := NewReader(bytes.NewReader(first), B)
	var i int
	for {
		rec, off, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		want := makeRecords()[i]
		if !bytes.Equal(rec, want) || off != offs1[i] {
			t.Fatalf("record %d mismatch", i)
		}
		i++
	}
	if i != len(makeRecords()) {
		t.Fatalf("read %d records, want %d", i, len(makeRecords()))
	}
	t.Logf("输入: 固定 12 条追加序列（含空记录/65536 字节）；输出 %d 字节两次完全一致，偏移=%d；判定依据: 无随机源，纯规则编码",
		len(first), offs1)
}
