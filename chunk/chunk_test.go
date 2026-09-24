package chunk

import (
	"errors"
	"maps"
	"math/rand"
	"slices"
	"testing"

	"ontology/wal"
)

func up(k int64, v string) wal.Entry { return wal.Entry{Op: wal.Upsert, Key: k, Val: v} }

func app(l *wal.Log, es ...wal.Entry) {
	for _, e := range es {
		l.Append(e)
	}
}

func cnt(o []Out, _ error) int { return len(o) }

func TestSection3(t *testing.T) {
	l := wal.New()
	m := New(l, 100)
	app(l, up(10, "a"), up(15, "b"), up(20, "x"))
	_ = m.BeginChunk(10, 20)
	app(l, up(15, "c"), wal.Entry{Op: wal.Delete, Key: 10})
	_ = m.ReadChunk()
	app(l, up(12, "d"), up(15, "e"), up(20, "y"))
	o1, _ := m.EndChunk()
	app(l, up(10, "f"), wal.Entry{Op: wal.Delete, Key: 12}, up(19, "g"))
	o2, _ := m.Poll()
	for i, tc := range []struct{ got, want []Out }{
		{o1, []Out{{wal.Upsert, 12, "d"}, {wal.Upsert, 15, "e"}}},
		{o2, []Out{{wal.Upsert, 10, "f"}, {wal.Delete, 12, ""}, {wal.Upsert, 19, "g"}}},
	} {
		if !slices.Equal(tc.got, tc.want) {
			t.Fatalf("批 %d 输出 %v != %v", i, tc.got, tc.want)
		}
	}
	if !maps.Equal(m.View(), map[int64]string{10: "f", 15: "e", 19: "g"}) {
		t.Fatalf("最终视图 %v", m.View())
	}
}

func TestRejectNoTrace(t *testing.T) {
	l := wal.New()
	m := New(l, 2)
	app(l, up(1, "a"), up(2, "b"), up(3, "c"))
	for _, tc := range []struct {
		want error
		op   func() error
	}{
		{ErrBadRange, func() error { return m.BeginChunk(5, 5) }},
		{ErrPhase, func() error { return m.ReadChunk() }},
		{nil, func() error { return m.BeginChunk(0, 10) }},
		{ErrPhase, func() error { return m.BeginChunk(20, 30) }},
		{ErrPhase, func() error { _, err := m.EndChunk(); return err }},
		{nil, func() error { return m.ReadChunk() }},
		{ErrViewLimit, func() error { _, err := m.EndChunk(); return err }},
	} {
		if err := tc.op(); !errors.Is(err, tc.want) {
			t.Fatalf("got %v want %v", err, tc.want)
		}
	}
	app(l, wal.Entry{Op: wal.Delete, Key: 3}) // 删一行后重试 EndChunk：成功且视图恰 2 行，证明被拒不留痕
	_, err := m.EndChunk()
	if len(m.View()) != 2 || err != nil || !errors.Is(m.BeginChunk(5, 15), ErrOverlap) {
		t.Fatal("拒绝后状态被改或无法继续")
	}
}

func TestCheckedNotLinearInM(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		l := wal.New()
		for i := range n {
			l.Append(up(int64(i%4000), "v"))
		}
		m := New(l, 1<<20)
		_ = m.BeginChunk(1000, 2000)
		app(l, up(1500, "x"), up(1501, "x"), wal.Entry{Op: wal.Delete, Key: 1502})
		_ = m.ReadChunk()
		app(l, up(1600, "y"), up(1601, "y"))
		if _, err := m.EndChunk(); err != nil {
			t.Fatal(err)
		}
		if m.checked > 7 { // H-L=5，允许加与 m 无关的小常数
			t.Fatalf("n=%d: checked=%d 随 m 增长", n, m.checked)
		}
	}
}

func TestConcurrent(t *testing.T) {
	l := wal.New()
	m := New(l, 1<<20)
	fin := make(chan struct{})
	go func() {
		defer close(fin)
		r := rand.New(rand.NewSource(5))
		for range 50000 {
			l.Append(wal.Entry{Op: wal.Op(r.Intn(2)), Key: r.Int63n(300), Val: "v"})
		}
	}()
	nOut := 0
	for i := 0; i < 5; i++ {
		_ = m.BeginChunk(int64(i*60), int64(i*60+60))
		_ = m.ReadChunk()
		nOut += cnt(m.EndChunk()) + cnt(m.Poll())
	}
	<-fin
	nOut += cnt(m.Poll())
	want := map[int64]string{}
	for k, v := range l.Table() {
		for _, d := range m.done {
			if k >= d.lo && k < d.hi {
				want[k] = v
			}
		}
	}
	if !maps.Equal(m.View(), want) {
		t.Fatal("并发下视图与源表不符")
	}
	exp := 0 // 不变量 3 的期望输出总数
	for _, d := range m.done {
		st := map[int64]bool{}
		for i, e := range l.Between(0, l.Pos()) {
			if int64(i) == d.h {
				exp += len(st) // H 时存在的键各一条
			}
			if e.Key < d.lo || e.Key >= d.hi {
				continue
			}
			if int64(i) >= d.h && (e.Op == wal.Upsert || st[e.Key]) {
				exp++
			}
			if e.Op == wal.Upsert {
				st[e.Key] = true
			} else {
				delete(st, e.Key)
			}
		}
		if d.h >= l.Pos() {
			exp += len(st)
		}
	}
	if nOut != exp {
		t.Fatalf("输出总数 %d != 应有 %d（重复或回退）", nOut, exp)
	}
}
