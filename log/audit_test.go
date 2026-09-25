package log

import (
	"errors"
	"reflect"
	"testing"

	"ontology/ent"
)

// TestSevenSteps 钉住第三节七行表：接受/拒绝、Seq、步后条目总数。
func TestSevenSteps(t *testing.T) {
	type c struct {
		ts       int64
		who, op  string
		seq, tot int64
		err      error
	}
	cs := []c{
		{10, "A", "put", 1, 2, nil}, {12, "A", "get", 2, 3, nil},
		{12, "B", "del", 3, 4, nil}, {9, "A", "put", -1, 4, ErrTSOutOfOrder},
		{15, "B", "put", 4, 5, nil}, {15, "", "get", -1, 5, ErrEmptyWho},
		{20, "A", "del", 5, 6, nil},
	}
	l := New()
	for i, x := range cs {
		seq, err := l.Append(x.ts, x.who, x.op)
		if err != nil {
			seq = -1
		}
		if seq != x.seq || !errors.Is(err, x.err) || int64(len(l.Entries())) != x.tot {
			t.Fatalf("step %d: seq=%d total=%d, want %d/%d", i+1, seq, len(l.Entries()), x.seq, x.tot)
		}
	}
	if l.Verify() != -1 {
		t.Fatal("final chain must verify")
	}
}

// TestSentinelErrors 四类错误互不相同、不留痕、拒绝后仍可正常使用。
func TestSentinelErrors(t *testing.T) {
	type c struct {
		ts      int64
		who, op string
		err     error
	}
	for _, x := range []c{{-1, "w", "op", ErrNegativeTS}, {0, "", "op", ErrEmptyWho}, {0, "w", "", ErrEmptyOp}} {
		l := New()
		n := len(l.Entries())
		if _, err := l.Append(x.ts, x.who, x.op); !errors.Is(err, x.err) || len(l.Entries()) != n {
			t.Fatalf("case %+v: error identity or no-trace violated", x)
		}
	}
	errs := []error{ErrNegativeTS, ErrEmptyWho, ErrEmptyOp, ErrTSOutOfOrder}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				t.Fatal("sentinel errors must be pairwise distinct")
			}
		}
	}
	l := New()
	_, _ = l.Append(5, "w", "op")
	if _, err := l.Append(4, "w", "op"); !errors.Is(err, ErrTSOutOfOrder) {
		t.Fatal("want out-of-order error")
	}
	if seq, err := l.Append(6, "w", "op"); err != nil || seq != 2 {
		t.Fatalf("log must stay usable after rejection, got seq=%d err=%v", seq, err)
	}
}

// TestVerifyTamper 钉住不变量 2/3；并在 pos=3 演示成对校验 (丙) 漏掉 4、5。
func TestVerifyTamper(t *testing.T) {
	for _, pos := range []int64{1, 2, 3, 4, 5} {
		l := buildChain(t, 5)
		l.entries[pos].Op = "x" // 只改字段，不动任何存储 Hash
		if l.Verify() != pos {
			t.Fatalf("tamper pos=%d: Verify=%d", pos, l.Verify())
		}
		aff := l.Affected(pos)
		if len(aff) != int(6-pos) || aff[0] != pos || aff[len(aff)-1] != 5 {
			t.Fatalf("tamper pos=%d: Affected=%v", pos, aff)
		}
		if pos == 3 { // 错误实现：每条只对前驱的「存储」Hash 做比对
			var flagged []int64
			for i := 1; i < len(l.entries); i++ {
				if ent.NextHash(l.entries[i-1].Hash, l.entries[i]) != l.entries[i].Hash {
					flagged = append(flagged, int64(i))
				}
			}
			if !reflect.DeepEqual(flagged, []int64{3}) {
				t.Fatalf("pairwise flags=%v, want [3] (misses 4,5)", flagged)
			}
		}
	}
	l := buildChain(t, 5)
	l.entries[2].Op, l.entries[4].Op = "x", "y" // 篡改多条返回最小者
	if l.Verify() != 2 {
		t.Fatalf("multi-tamper Verify=%d, want 2", l.Verify())
	}
}

func buildChain(t *testing.T, n int) *Log {
	t.Helper()
	l := New()
	for i := 1; i <= n; i++ {
		if _, err := l.Append(int64(i*10), "w", "op"); err != nil {
			t.Fatal(err)
		}
	}
	return l
}
