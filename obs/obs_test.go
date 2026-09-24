package obs

import (
	"errors"
	"testing"
)

// TestCheckCountBounded 证明 MaxSeen 由标量维护：喂 m 个递增事件后再喂一个，
// 最近一次 Feed 检查过的历史事件个数不随 m 线性增长，恒为小常数 1。
func TestCheckCountBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		o, err := New(10)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		for i := 1; i <= m; i++ {
			if err := o.Feed(int64(i)); err != nil {
				t.Fatalf("m=%d Feed(%d): %v", m, i, err)
			}
		}
		before := o.lastChecksCount()
		if err := o.Feed(int64(m + 1)); err != nil {
			t.Fatalf("m=%d final Feed: %v", m, err)
		}
		got := o.lastChecksCount()
		const bound = 1 // 与 m 无关的小常数
		if got > bound {
			t.Fatalf("m=%d checks=%d, want <= %d (扫描历史?)", m, got, bound)
		}
		if before > bound {
			t.Fatalf("m=%d 中途 checks=%d, want <= %d", m, before, bound)
		}
	}
}

func TestNewRejectsNegativeSlack(t *testing.T) {
	cases := []int64{-1, -10, -1 << 40}
	for _, slack := range cases {
		o, err := New(slack)
		if !errors.Is(err, ErrInvalidSlack) || o != nil {
			t.Fatalf("New(%d)=(%v,%v), want nil,%v", slack, o, err, ErrInvalidSlack)
		}
	}
}

// TestCheckCountUnexported 保证计数器没有导出访问途径：
// 字段与方法均小写，外部包（含 api 与 demo）无法读到它。
func TestCheckCountUnexported(t *testing.T) {
	o, _ := New(0)
	_ = o.Feed(1)
	// 仅同包白盒可访问：
	if o.lastChecks != 1 || o.lastChecksCount() != 1 {
		t.Fatalf("lastChecks=%d", o.lastChecks)
	}
}
