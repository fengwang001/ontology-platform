package tob

import (
	"errors"
	"fmt"
	"testing"
)

func pmsg(s int) string { return string(rune('A' + s - 1)) }
func must(t *testing.T, ok bool, f string, a ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(f, a...)
	}
}

// crashSetup 造 d=2、nextSeq=6、空洞 3,4,5（即十步序列的第 1-8 步）。
func crashSetup(l *Log) {
	for _, p := range []string{"A", "B", "C", "D", "E"} {
		l.Propose(p)
	}
	l.Deliver()
	l.Deliver()
	l.Crash()
}

// tenSteps 执行第三节十步序列，refill 是补发到达顺序（3,4,5 的某种排列）。
func tenSteps(t *testing.T, refill []int) (*Log, []string) {
	t.Helper()
	l := New()
	crashSetup(l)
	for _, s := range refill {
		l.RePropose(s, pmsg(s))
	}
	got := []string{"1:A", "2:B"}
	for _, s := range []int{3, 4, 5} {
		qs, p, ok := l.Deliver()
		must(t, ok && p == pmsg(s) && qs == s, "refill=%v seq %d got %d:%s", refill, s, qs, p)
		got = append(got, fmt.Sprintf("%d:%s", qs, p))
	}
	return l, got
}

// checkABCDE：十步后必须恰好按序投出 A..E；枚举 3,4,5 全部 6 种补发到达排列。
func checkABCDE(t *testing.T) {
	t.Helper()
	want := []string{"1:A", "2:B", "3:C", "4:D", "5:E"}
	perms := [][]int{{3, 4, 5}, {3, 5, 4}, {4, 3, 5}, {4, 5, 3}, {5, 3, 4}, {5, 4, 3}}
	for _, refill := range perms {
		l, got := tenSteps(t, refill)
		must(t, l.Delivered() == 5 && len(got) == 5, "refill=%v d=%d got=%v", refill, l.Delivered(), got)
		for i, w := range want {
			must(t, got[i] == w, "refill=%v pos %d: %s != %s", refill, i+1, got[i], w)
		}
	}
}

// I1：投递恒为 1..d 连续前缀（乱序补发亦然）；空洞未补时 Deliver 不跳号。
func TestInvariantContiguousPrefix(t *testing.T) {
	checkABCDE(t)
	l := New()
	l.Propose("x")
	l.Propose("y")
	l.Deliver()
	l.Crash() // d=1，seq2 丢失
	_, _, ok := l.Deliver()
	must(t, !ok && l.Delivered() == 1, "crossed hole ok=%v d=%d", ok, l.Delivered())
}

// I2：与朴素重放一致——不丢、不重、顺序不变（want 即崩溃前日志按 seq 重放）。
func TestReplayEquivalence(t *testing.T) { checkABCDE(t) }

// I3：nextSeq、d 单调不回退；补发沿用原 seq、不推进 nextSeq，之后新分配继续。
func TestMonotonicAndOriginalSeq(t *testing.T) {
	l := New()
	for want := 1; want <= 5; want++ {
		s, _ := l.Propose("p")
		must(t, s == want, "alloc got %d want %d", s, want)
	}
	l.Deliver()
	l.Deliver()
	l.Crash()
	must(t, l.nextSeq() == 6 && l.Delivered() == 2, "n=%d d=%d", l.nextSeq(), l.Delivered())
	for _, s := range []int{3, 5, 4} {
		e := l.RePropose(s, pmsg(s))
		must(t, e == nil && l.nextSeq() == 6, "repropose %d e=%v n=%d", s, e, l.nextSeq())
	}
	s, _ := l.Propose("F")
	must(t, s == 6, "post-refill alloc got %d", s)
}

// I4：四类拒绝互不相同、整体失败、状态不留痕，之后仍可正常使用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	sen := []error{ErrEmptyPayload, ErrAlreadyDelivered, ErrSeqOutOfRange, ErrSlotFilled}
	for i := 1; i < 4; i++ {
		for j := 0; j < i; j++ {
			must(t, !errors.Is(sen[i], sen[j]), "duplicate sentinel %d", i)
		}
	}
	rep := func(s int, p string) func(*Log) error { return func(l *Log) error { return l.RePropose(s, p) } }
	cases := []struct {
		name  string
		setup func(*Log)
		call  func(*Log) error
		want  error
	}{
		{"empty", func(l *Log) { l.Propose("A") },
			func(l *Log) error { _, e := l.Propose(""); return e }, ErrEmptyPayload},
		{"delivered", crashSetup, rep(2, "x"), ErrAlreadyDelivered},
		{"range", crashSetup, rep(6, "x"), ErrSeqOutOfRange},
		{"filled", func(l *Log) { crashSetup(l); l.RePropose(3, "C") },
			rep(3, "X"), ErrSlotFilled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := New()
			tc.setup(l)
			d0, n0 := l.Delivered(), l.nextSeq()
			e := tc.call(l)
			must(t, errors.Is(e, tc.want), "e=%v want %v", e, tc.want)
			must(t, l.Delivered() == d0 && l.nextSeq() == n0, "rejected op changed state")
			_, err := l.Propose("z") // 新 Propose 恒可用
			must(t, err == nil, "unusable after reject: %v", err)
		})
	}
}

// O(1)：单次 Deliver 直接定位的条目数恒为 1，与日志规模 m 无关。
func TestDeliverO1Scan(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		l := New()
		for i := 0; i < m; i++ {
			l.Propose("x")
		}
		s, _, ok := l.Deliver()
		must(t, ok && s == 1 && l.lastScan == 1, "m=%d s=%d scan=%d", m, s, l.lastScan)
		_, _, ok = l.Deliver()
		must(t, ok && l.lastScan == 1, "m=%d second scan=%d", m, l.lastScan)
	}
	_, _, ok := New().Deliver() // 空日志只定位一个槽且不投递
	must(t, !ok, "empty log delivered")
}
