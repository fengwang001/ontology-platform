package resolve_test

import (
	"errors"
	"testing"

	"ontology/alias"
	"ontology/indexreg"
	"ontology/resolve"
)

func TestResolveIndexAndNotFound(t *testing.T) {
	m := alias.NewManager(indexreg.New())
	if err := m.CreateIndex("i1"); err != nil {
		t.Fatal(err)
	}
	r := resolve.New()

	rs, err := r.Read(m.SnapshotView(), "i1")
	if err != nil || len(rs) != 1 || rs[0].Index != "i1" {
		t.Fatalf("Read index = %v,%v", rs, err)
	}
	w, err := r.Write(m.SnapshotView(), "i1", resolve.NewTracer())
	if err != nil || w != "i1" {
		t.Fatalf("Write index = %q,%v", w, err)
	}
	if _, err := r.Read(m.SnapshotView(), "nope"); !errors.Is(err, resolve.ErrNameNotFound) {
		t.Fatalf("Read missing: %v", err)
	}
	if _, err := r.Write(m.SnapshotView(), "nope", resolve.NewTracer()); !errors.Is(err, resolve.ErrNameNotFound) {
		t.Fatalf("Write missing: %v", err)
	}
}

func TestClosedAsymmetryAndNoFallback(t *testing.T) {
	m := alias.NewManager(indexreg.New())
	for _, n := range []string{"i1", "i2"} {
		if err := m.CreateIndex(n); err != nil {
			t.Fatal(err)
		}
	}
	r := resolve.New()

	// a -> i1(未指定, filter=f1), i2(真)。写索引 i2。
	if err := m.Update([]alias.Action{
		alias.Add("a", "i1", alias.WriteUnspecified, "f1"),
		alias.Add("a", "i2", alias.WriteTrue, ""),
	}); err != nil {
		t.Fatal(err)
	}

	// 关闭 i1：读静默跳过 i1，只剩 i2，filter 保留。
	if err := m.CloseIndex("i1"); err != nil {
		t.Fatal(err)
	}
	rs, err := r.Read(m.SnapshotView(), "a")
	if err != nil || len(rs) != 1 || rs[0].Index != "i2" {
		t.Fatalf("read after close i1 = %v,%v", rs, err)
	}

	// 关闭写索引 i2：写报已关闭且不回落到未关闭成员（此时无未关闭成员）。
	if err := m.CloseIndex("i2"); err != nil {
		t.Fatal(err)
	}
	rs, err = r.Read(m.SnapshotView(), "a")
	if err != nil || len(rs) != 0 {
		t.Fatalf("all closed read = %v,%v, want empty no error", rs, err)
	}
	tracer := resolve.NewTracer()
	if _, err := r.Write(m.SnapshotView(), "a", tracer); !errors.Is(err, indexreg.ErrIndexClosed) {
		t.Fatalf("write closed write-index: %v", err)
	}
	if tracer.Touched() > 1 {
		t.Fatalf("touched = %d, want <= 1", tracer.Touched())
	}

	// 打开 i1：写索引仍为已关闭的 i2（不回落），读回到 i1 且带 filter。
	if err := m.OpenIndex("i1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write(m.SnapshotView(), "a", resolve.NewTracer()); !errors.Is(err, indexreg.ErrIndexClosed) {
		t.Fatalf("write must not fall back to open i1: %v", err)
	}
	rs, _ = r.Read(m.SnapshotView(), "a")
	if len(rs) != 1 || rs[0].Index != "i1" || rs[0].Filter != "f1" {
		t.Fatalf("read reopened = %+v", rs)
	}
}
