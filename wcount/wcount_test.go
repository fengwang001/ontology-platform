package wcount

import (
	"fmt"
	"testing"
)

// TestLookupCost 钉住复杂度约束：先让 m 个不同 Key 各有高水位，
// 再喂一个属于其中某 Key 的事件，断言为定位高水位而检查的 Key 个数
// 不随 m 线性增长（map 定位，恒为 1）。内部测试直接读非导出字段 checked。
func TestLookupCost(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			c := New(3, m+1)
			evs := make([]Event, 0, m)
			for i := 0; i < m; i++ {
				evs = append(evs, Event{Key: fmt.Sprintf("key-%d", i), Seq: int64(i * 10)})
			}
			if err := c.Feed(evs); err != nil {
				t.Fatalf("seed feed: %v", err)
			}
			// 命中已有 Key 的事件：定位只许检查常数个 Key。
			if err := c.Feed([]Event{{Key: "key-0", Seq: int64(m * 10)}}); err != nil {
				t.Fatalf("probe feed: %v", err)
			}
			if c.checked > 1 {
				t.Fatalf("m=%d: checked %d keys, want <= 1 (map lookup, not linear scan)", m, c.checked)
			}
			// 新 Key 的事件同样只检查常数个。
			if err := c.Feed([]Event{{Key: "key-new", Seq: 1}}); err != nil {
				t.Fatalf("new-key feed: %v", err)
			}
			if c.checked > 1 {
				t.Fatalf("m=%d: new key checked %d keys, want <= 1", m, c.checked)
			}
		})
	}
}

// TestBatchAtomic 钉住 wcount 层整批失败不留痕。
func TestBatchAtomic(t *testing.T) {
	cases := []struct {
		name  string
		batch []Event
		want  error
	}{
		{"empty key first", []Event{{Key: "", Seq: 1}, {Key: "b", Seq: 2}}, ErrEmptyKey},
		{"empty key last", []Event{{Key: "b", Seq: 2}, {Key: "", Seq: 1}}, ErrEmptyKey},
		{"too many keys", []Event{{Key: "b", Seq: 1}, {Key: "c", Seq: 2}}, ErrTooManyKeys},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(3, 2) // 已有 "a" 后只剩 1 个空位
			if err := c.Feed([]Event{{Key: "a", Seq: 10}}); err != nil {
				t.Fatal(err)
			}
			if err := c.Feed(tc.batch); err != tc.want {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if c.Accepted("b") != 0 || c.Accepted("c") != 0 || c.Dropped() != 0 {
				t.Fatal("rejected batch left traces")
			}
			if h, ok := c.High("a"); !ok || h != 10 {
				t.Fatal("existing key state changed")
			}
			if err := c.Feed([]Event{{Key: "b", Seq: 1}}); err != nil {
				t.Fatalf("unusable after rejection: %v", err)
			}
		})
	}
}
