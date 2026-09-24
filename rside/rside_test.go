package rside

import (
	"fmt"
	"testing"
)

// TestSubscriberLookup 证明右表变更按 fk 定位订阅者，而非扫描全部订阅：
// m 个左行订阅 m 个互不相同的 fk，另有 1 个左行订阅目标 fk；
// 对目标 fk 的 Put 检查过的订阅条目数不随 m 增长。
func TestSubscriberLookup(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			r := New(2 * (m + 1))
			for i := 0; i < m; i++ {
				if err := r.Subscribe(fmt.Sprintf("fk%d", i), fmt.Sprintf("k%d", i), uint64(i)); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Subscribe("target", "kt", 99); err != nil {
				t.Fatal(err)
			}
			if err := r.Put("target", "v1"); err != nil {
				t.Fatal(err)
			}
			const subscribersOfTarget = 1
			if r.checked > subscribersOfTarget {
				t.Fatalf("checked %d entries, grows with m=%d; want <= %d", r.checked, m, subscribersOfTarget)
			}
			// 目标 fk 的订阅者确实收到了响应（队首是订阅响应，其次是变更响应）
			if _, err := r.Deliver("target"); err != nil {
				t.Fatal(err)
			}
			resp, err := r.Deliver("target")
			if err != nil || resp.K != "kt" || resp.RVal != "v1" {
				t.Fatalf("target subscriber not notified: %+v, %v", resp, err)
			}
		})
	}
}

// TestRightChangeAtomic 右表变更要么全部响应入队，要么一条都不入队。
func TestRightChangeAtomic(t *testing.T) {
	r := New(2)
	_ = r.Put("A", "a0") // 尚无订阅者，直接成功
	_ = r.Subscribe("A", "k1", 1)
	_ = r.Subscribe("A", "k2", 2) // pending=2
	if err := r.Put("A", "a1"); err != ErrTooManyPending {
		t.Fatalf("want ErrTooManyPending, got %v", err)
	}
	if len(r.PendingFKs()) != 1 { // 只有订阅时的 2 条，没有新增
		t.Fatalf("partial enqueue happened")
	}
	if r.tab["A"] != "a0" {
		t.Fatalf("rejected Put changed right table")
	}
	if err := r.Delete("A"); err != ErrTooManyPending { // 删除同样需要 2 条额度
		t.Fatalf("want ErrTooManyPending, got %v", err)
	}
	if r.tab["A"] != "a0" {
		t.Fatalf("rejected Delete changed right table")
	}
}

// TestSentinelsDistinct 三类哨兵错误互不相同。
func TestSentinelsDistinct(t *testing.T) {
	if len(map[error]bool{ErrEmptyKey: true, ErrEmptyQueue: true, ErrTooManyPending: true}) != 3 {
		t.Fatal("sentinel errors not distinct")
	}
}
