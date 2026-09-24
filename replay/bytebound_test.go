package replay

import (
	"os"
	"testing"

	"ontology/segment"
	"ontology/sparse"
)

const recordSize = 4 + (8 + len("payload")) + 4 // len + event + crc

func TestByteBound(t *testing.T) {
	const total, n = 2000, 128
	dir := t.TempDir()
	writeSegments(t, dir, total, 500) // 跨 4 段
	buildIndexes(t, dir, n)
	from, to := uint64(700), uint64(1300)
	res, err := New(dir, n).Range(from, to)
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(res.Events)) != to-from+1 {
		t.Fatalf("count=%d", len(res.Events))
	}
	want := int64(len(res.Events))*recordSize + int64(n)*recordSize
	if res.Stats.Bytes > want {
		t.Fatalf("bytes=%d > bound=%d", res.Stats.Bytes, want)
	}
}

func TestTamperedIndexFallback(t *testing.T) {
	dir := t.TempDir()
	writeSegments(t, dir, 40, 40)
	p := segment.Path(dir, 1)
	idx, err := sparse.Build(p, 8)
	if err != nil {
		t.Fatal(err)
	}
	anchors := idx.Anchors()
	// 把序号 8 的锚点偏移改到该记录中间。
	anchors[1] = sparse.Anchor{Seq: anchors[1].Seq, Offset: anchors[1].Offset + 3}
	tampered := sparse.New(8, anchors)
	if err := tampered.Save(p); err != nil {
		t.Fatal(err)
	}
	res, err := New(dir, 8).Range(10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 11 || res.Events[0].Seq != 10 || res.Events[10].Seq != 20 {
		t.Fatalf("wrong result: %d events", len(res.Events))
	}
	if !res.Stats.Fallback || !res.Stats.Stale {
		t.Fatalf("expected fallback+stale flags, got %+v", res.Stats)
	}
	// 未触及被篡改锚点的区间仍可正常定位。
	ok, err := New(dir, 8).Range(0, 3)
	if err != nil || ok.Stats.Stale || len(ok.Events) != 4 {
		t.Fatalf("clean anchor region: %+v stale=%v err=%v", ok.Events, ok.Stats.Stale, err)
	}
}

func TestIndexFileRemovedRebuilds(t *testing.T) {
	dir := t.TempDir()
	writeSegments(t, dir, 20, 20)
	p := segment.Path(dir, 1)
	if err := os.Remove(segment.IndexPath(p)); err != nil {
		t.Fatal(err)
	}
	res, err := New(dir, 8).Range(5, 15)
	if err != nil || len(res.Events) != 11 {
		t.Fatalf("rebuild replay: %d err=%v", len(res.Events), err)
	}
}
