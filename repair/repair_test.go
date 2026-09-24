package repair

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
	"ontology/segment"
)

const (
	testEvents = 500
	testRecLen = 4 + 16 + 4 // payload 4B: len4 + event16 + crc4
)

func buildSeg(t *testing.T, dir string, first uint64) string {
	t.Helper()
	p := filepath.Join(dir, fmt.Sprintf("%020d.seg", first))
	w, err := segment.Create(p, first)
	if err != nil {
		t.Fatal(err)
	}
	for i := uint64(0); i < testEvents; i++ {
		if err := w.Append(event.Event{Seq: first + i, Payload: []byte("abcd")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func wantClass(t int64) error {
	if t < segment.HeaderSize {
		return segment.ErrHeaderIncomplete
	}
	r := (t - segment.HeaderSize) % testRecLen
	switch {
	case r == 0:
		return nil // 干净记录边界
	case r <= 3:
		return segment.ErrLengthIncomplete
	case r <= 19:
		return segment.ErrBodyIncomplete
	default:
		return segment.ErrCRCMismatch
	}
}

func TestTruncationClassification(t *testing.T) {
	dir := t.TempDir()
	seg := buildSeg(t, dir, 0)
	data, err := os.ReadFile(seg)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != segment.HeaderSize+testEvents*testRecLen {
		t.Fatalf("file size %d", len(data))
	}
	tmp := filepath.Join(dir, "cut.seg")
	counts := map[error]int{}
	for cut := int64(1); cut < int64(len(data)); cut++ {
		if err := os.WriteFile(tmp, data[:cut], 0o644); err != nil {
			t.Fatal(err)
		}
		err := Classify(tmp)
		want := wantClass(cut)
		if !errors.Is(err, want) {
			t.Fatalf("cut=%d: got %v want %v", cut, err, want)
		}
		counts[want]++
	}
	for _, e := range []error{segment.ErrHeaderIncomplete, segment.ErrLengthIncomplete,
		segment.ErrBodyIncomplete, segment.ErrCRCMismatch} {
		if counts[e] == 0 {
			t.Fatalf("class %v never observed", e)
		}
	}
}

func TestRepairSegment(t *testing.T) {
	dir := t.TempDir()
	seg := buildSeg(t, dir, 0)
	data, _ := os.ReadFile(seg)
	cut := int64(segment.HeaderSize + 251*testRecLen + 7) // 第 252 条记录体内
	if err := os.WriteFile(seg, data[:cut], 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := RepairSegment(seg)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Before != testEvents || rep.After != 251 {
		t.Fatalf("report %+v, want before=500 after=251", rep)
	}
	if rep.Before-rep.After != 249 {
		t.Fatalf("diff=%d want 249", rep.Before-rep.After)
	}
	if err := Classify(seg); err != nil {
		t.Fatalf("after repair: %v", err)
	}
	f, _ := os.Open(seg)
	defer f.Close()
	h, err := segment.ReadHeader(f)
	if err != nil {
		t.Fatal(err)
	}
	if h.Count != rep.After {
		t.Fatalf("header count %d want %d", h.Count, rep.After)
	}
	var n uint64
	if err := segment.Scan(f, segment.HeaderSize, func(_ int64, e event.Event, _ int) error {
		if e.Seq != n {
			t.Fatalf("seq %d want %d", e.Seq, n)
		}
		n++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != rep.After {
		t.Fatalf("rescanned %d want %d", n, rep.After)
	}
}

func TestSeqGapAndRebuild(t *testing.T) {
	dir := t.TempDir()
	buildSeg(t, dir, 0)
	p2 := filepath.Join(dir, fmt.Sprintf("%020d.seg", 501)) // 缺序号 500
	w, err := segment.Create(p2, 501)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(event.Event{Seq: 501, Payload: []byte("abcd")}); err != nil {
		t.Fatal(err)
	}
	w.Close()
	err = CheckContinuity(dir)
	var ge *GapError
	if !errors.Is(err, ErrSeqGap) || !errors.As(err, &ge) {
		t.Fatalf("err=%v, want ErrSeqGap", err)
	}
	if ge.Lo != 500 || ge.Hi != 500 {
		t.Fatalf("gap [%d,%d], want [500,500]", ge.Lo, ge.Hi)
	}
	// 索引重建：删除后与原件逐字节相同
	seg := filepath.Join(dir, fmt.Sprintf("%020d.seg", 0))
	if err := RebuildIndex(seg, 8); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.ReadFile(seg + ".idx")
	os.Remove(seg + ".idx")
	if err := RebuildIndex(seg, 8); err != nil {
		t.Fatal(err)
	}
	rebuilt, _ := os.ReadFile(seg + ".idx")
	if string(orig) != string(rebuilt) {
		t.Fatal("rebuilt index differs")
	}
}
