package trunc

import (
	"errors"
	"testing"

	"ontology/log"
)

func appendN(l *log.Log, n int) {
	for i := 0; i < n; i++ {
		l.Append("x")
	}
}

// TestCPReadCount 钉死复杂度：合法 Truncate 判 K<=cp 只读 cp 一个字段，与 m 无关。
func TestCPReadCount(t *testing.T) {
	for _, m := range []uint64{100, 1000, 5000, 10000} {
		l := log.New()
		tr := New(l)
		appendN(l, int(m)+1)
		if err := l.Checkpoint(m); err != nil {
			t.Fatalf("m=%d cp: %v", m, err)
		}
		if err := tr.Truncate(m); err != nil {
			t.Fatalf("m=%d trunc: %v", m, err)
		}
		if tr.cpReads > 1 {
			t.Errorf("m=%d: cpReads=%d, want <= 1 (O(1), independent of m)", m, tr.cpReads)
		}
	}
}

// TestTruncateBeyondCP：K>cp 一律拒绝且不留痕；合法 K 成功后 tm==f==K。
func TestTruncateBeyondCP(t *testing.T) {
	cases := []struct {
		name    string
		n       int
		cp      int64 // -1：不做 Checkpoint
		preK, k uint64
		reject  bool
	}{
		{"empty-nocp", 0, -1, 0, 0, true}, {"three-nocp", 3, -1, 0, 1, true},
		{"cp2-k3", 4, 2, 0, 3, true}, {"cp2-k2-legal", 4, 2, 0, 2, false},
		{"after-legal-k3", 4, 2, 2, 3, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := log.New()
			tr := New(l)
			appendN(l, c.n)
			if c.cp >= 0 {
				l.Checkpoint(uint64(c.cp))
			}
			if c.preK > 0 {
				tr.Truncate(c.preK)
			}
			tm, f, n := tr.Marker(), l.First(), len(l.Read(0))
			err := tr.Truncate(c.k)
			if c.reject {
				if !errors.Is(err, ErrTruncateBeyondCP) ||
					tr.Marker() != tm || l.First() != f || len(l.Read(0)) != n {
					t.Fatalf("Truncate(%d): err=%v or rejected op left a trace", c.k, err)
				}
				return
			}
			if err != nil || tr.Marker() != c.k || l.First() != c.k {
				t.Fatalf("legal Truncate(%d): err=%v tm=%d f=%d", c.k, err, tr.Marker(), l.First())
			}
		})
	}
}

type row struct {
	cp            int64
	tm, f, lo, hi uint64
}

// TestEightStepCrash：NOTES.md 八行表逐行钉死，并覆盖恢复双判定三情形。
func TestEightStepCrash(t *testing.T) {
	l := log.New()
	tr := New(l)
	ok := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []row{{-1, 0, 0, 0, 0}, {-1, 0, 0, 0, 1}, {-1, 0, 0, 0, 2}, {2, 0, 0, 0, 2}, {2, 2, 2, 2, 2}, {2, 2, 2, 2, 3}, {3, 2, 2, 2, 3}, {3, 3, 2, 2, 3}}
	chk := func(i int) {
		es := l.Read(0)
		got := row{l.CP(), tr.Marker(), l.First(), es[0].Offset, es[len(es)-1].Offset}
		if got != want[i] {
			t.Fatalf("step %d: %+v want %+v", i+1, got, want[i])
		}
	}
	l.Append("a")
	chk(0)
	l.Append("b")
	chk(1)
	l.Append("c")
	chk(2)
	ok(l.Checkpoint(2))
	chk(3)
	ok(tr.Truncate(2))
	chk(4) // K=2 <= cp=2
	l.Append("d")
	chk(5)
	ok(l.Checkpoint(3))
	chk(6)
	ok(tr.Mark(3))
	chk(7) // 写标记后、物理删除前崩溃
	ok(tr.Recover(tr.Marker(), l.First()))
	if tr.Marker() != 3 || l.First() != 3 {
		t.Fatalf("converge: tm=%d f=%d, want 3/3", tr.Marker(), l.First())
	}
	if es := l.Read(0); len(es) != 1 || es[0].Offset != 3 || es[0].Payload != "d" {
		t.Fatalf("post-recovery visible set: %+v", es)
	}
	for _, s := range []struct {
		name                         string
		marker, first, wantTM, wantF uint64
		reject                       bool
	}{
		{"clean", 3, 3, 3, 3, false}, {"interrupted", 3, 2, 3, 3, false},
		{"overdeleted", 2, 3, 2, 3, true},
	} {
		l2 := log.New()
		tr2 := New(l2)
		appendN(l2, 4)
		l2.Checkpoint(3)
		tr2.Truncate(2)
		switch s.name {
		case "clean":
			tr2.Truncate(3)
		case "interrupted":
			tr2.Mark(3)
		case "overdeleted": // 错误的“先删后标”：tm 仍旧值 2，f 已=3
			l2.DeleteBefore(3)
		}
		err := tr2.Recover(s.marker, s.first)
		if (s.reject && !errors.Is(err, ErrRecoverOverDeleted)) || (!s.reject && err != nil) {
			t.Fatalf("%s recover: %v", s.name, err)
		}
		if tr2.Marker() != s.wantTM || l2.First() != s.wantF {
			t.Fatalf("%s: tm=%d f=%d, want %d/%d", s.name, tr2.Marker(), l2.First(), s.wantTM, s.wantF)
		}
	}
}
