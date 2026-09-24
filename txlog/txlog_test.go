package txlog

import (
	"errors"
	"testing"
)

// 第四节：为确定 LSO 检查的事务个数不随未决事务总数 m 线性增长。
func TestLSOCounterBounded(t *testing.T) {
	const bound = 4 // 与 m 无关的小常数
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		l := New()
		for p := 1; p <= m; p++ {
			if _, err := l.AppendData(p, "v"); err != nil {
				t.Fatal(err)
			}
		}
		if err := l.AdvanceHW(m); err != nil { // 覆盖全部，此步不计
			t.Fatal(err)
		}
		if _, err := l.AppendCommit(m); err != nil { // 首位点最晚的生产者
			t.Fatal(err)
		}
		if err := l.AdvanceHW(m + 1); err != nil {
			t.Fatal(err)
		}
		if l.checked > bound {
			t.Fatalf("m=%d: 最晚提交后 checked=%d > %d", m, l.checked, bound)
		}
		if _, err := l.AppendCommit(1); err != nil { // 首位点最早的生产者
			t.Fatal(err)
		}
		if err := l.AdvanceHW(m + 2); err != nil {
			t.Fatal(err)
		}
		if l.checked > bound {
			t.Fatalf("m=%d: 最早提交后 checked=%d > %d", m, l.checked, bound)
		}
		if got := l.LSO(); got != 1 {
			t.Fatalf("m=%d: LSO=%d, 应为第二个生产者的首位点 1", m, got)
		}
	}
}

// 第五节（txlog 层）：三类哨兵错误可判定且被拒后状态不变。
func TestTxlogErrors(t *testing.T) {
	cases := []struct {
		name string
		op   func(l *Log) error
		want error
	}{
		{"非正 pid 数据", func(l *Log) error { _, e := l.AppendData(0, "x"); return e }, ErrInvalidRecord},
		{"非正 pid 控制", func(l *Log) error { _, e := l.AppendCommit(-1); return e }, ErrInvalidRecord},
		{"无进行中事务 Commit", func(l *Log) error { _, e := l.AppendCommit(9); return e }, ErrNoOpenTxn},
		{"无进行中事务 Abort", func(l *Log) error { _, e := l.AppendAbort(9); return e }, ErrNoOpenTxn},
		{"HW 回退", func(l *Log) error { return l.AdvanceHW(0) }, ErrInvalidHW},
		{"HW 超末端", func(l *Log) error { return l.AdvanceHW(3) }, ErrInvalidHW},
	}
	// 三类哨兵互不相同
	if ErrInvalidRecord == ErrNoOpenTxn || ErrNoOpenTxn == ErrInvalidHW || ErrInvalidRecord == ErrInvalidHW {
		t.Fatal("哨兵错误不互不相同")
	}
	for _, c := range cases {
		l := New()
		if _, err := l.AppendData(1, "a"); err != nil {
			t.Fatal(err)
		}
		if err := l.AdvanceHW(1); err != nil {
			t.Fatal(err)
		}
		hw, lso := l.HW(), l.LSO()
		if err := c.op(l); !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v, want %v", c.name, err, c.want)
		}
		if l.HW() != hw || l.LSO() != lso {
			t.Fatalf("%s: 被拒后 HW/LSO 改变", c.name)
		}
		off, err := l.AppendData(1, "b") // 日志未变：下一条位点仍为 1
		if err != nil || off != 1 {
			t.Fatalf("%s: 被拒后日志改变 off=%d err=%v", c.name, off, err)
		}
	}
}
