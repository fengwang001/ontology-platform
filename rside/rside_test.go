package rside

import (
	"fmt"
	"strconv"
	"testing"
)

// TestSubscriberCheckCountScalesWithFKOnly 钉住复杂度（第四节）：
// m 个左行订阅 m 个互异 fk，另有 1 个左行订阅目标 fk；对目标 fk 一次 ChangeRight，
// 被检查的订阅条目数必须恒等于目标 fk 订阅者数（1），不随 m 线性增长。
func TestSubscriberCheckCountScalesWithFKOnly(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		r := New()
		for i := 0; i < m; i++ {
			r.Subscribe("fk"+strconv.Itoa(i), "k"+strconv.Itoa(i), uint64(i))
		}
		r.Subscribe("target", "kt", 999)
		v := "v"
		n := r.ChangeRight("target", &v)
		if r.lastChecked != 1 || n != 1 { // lastChecked 与 m 无关：只查目标 fk 的 1 个订阅者
			t.Fatalf("m=%d: checked=%d responses=%d, want 1,1（不随 m 增长）", m, r.lastChecked, n)
		}
		if r.QueueLen("target") != 2 || r.Pending() != m+2 {
			t.Fatalf("m=%d: targetQueue=%d pending=%d want 2,%d", m, r.QueueLen("target"), r.Pending(), m+2)
		}
	}
}

// TestCheckCountEqualsTargetSubscribers 多订阅者档：检查数恰为目标 fk 订阅者数。
func TestCheckCountEqualsTargetSubscribers(t *testing.T) {
	for _, subs := range []int{1, 2, 7, 50} {
		r := New()
		for i := 0; i < 1000; i++ {
			r.Subscribe("other"+strconv.Itoa(i), "ok"+strconv.Itoa(i), 1)
		}
		for i := 0; i < subs; i++ {
			r.Subscribe("target", "t"+strconv.Itoa(i), 2)
		}
		v := "rv"
		r.ChangeRight("target", &v)
		if r.lastChecked != subs {
			t.Fatalf("subs=%d: checked=%d want %d", subs, r.lastChecked, subs)
		}
	}
}

// TestFIFOAndLexicographic 表驱动：同 fk 严格 FIFO；右表变更按 k 字典序产生响应。
func TestFIFOAndLexicographic(t *testing.T) {
	cases := []struct {
		name string
		run  func(r *RSide) []string
		want []string
	}{
		{"subscribe-immediate-nil-then-value", func(r *RSide) []string {
			r.Subscribe("F", "k1", 1) // 右表无 F：立即 nil
			v := "a"
			r.ChangeRight("F", &v)
			return drain(r, "F")
		}, []string{"k1:nil", "k1:a"}},
		{"change-emits-lexicographic", func(r *RSide) []string {
			r.Subscribe("F", "k3", 1) // 订阅即时 nil（按订阅顺序入队）
			r.Subscribe("F", "k1", 1)
			r.Subscribe("F", "k2", 1)
			r.ChangeRight("F", nil) // 删除按 k 字典序
			return drain(r, "F")
		}, []string{"k3:nil", "k1:nil", "k2:nil", "k1:nil", "k2:nil", "k3:nil"}},
		{"unsubscribe-removes-only-target", func(r *RSide) []string {
			r.Subscribe("F", "k1", 1)
			r.Subscribe("F", "k2", 1)
			r.Unsubscribe("F", "k1")
			v := "b"
			r.ChangeRight("F", &v)
			return drain(r, "F")
		}, []string{"k1:nil", "k2:nil", "k2:b"}},
		{"separate-fk-queues", func(r *RSide) []string {
			r.Subscribe("A", "ka", 1)
			r.Subscribe("B", "kb", 1)
			return append(drain(r, "B"), drain(r, "A")...)
		}, []string{"kb:nil", "ka:nil"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			if got := tc.run(r); fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

// TestDeleteRightKeepsSubscription 右表删除不删订阅：复活后仍向原订阅者发响应。
func TestDeleteRightKeepsSubscription(t *testing.T) {
	r := New()
	v := "a"
	r.ChangeRight("F", &v)
	r.Subscribe("F", "k1", 7) // 立即 a
	r.ChangeRight("F", nil)   // 删除：订阅保留，发 nil
	r.ChangeRight("F", &v)    // 复活：仍向 k1 发 a
	want := []string{"k1:a", "k1:nil", "k1:a"}
	if got := drain(r, "F"); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	if r.SubsLen("F") != 1 {
		t.Fatalf("subscription removed by right delete: subs=%d", r.SubsLen("F"))
	}
}

func drain(r *RSide, fk string) []string {
	var out []string
	for {
		resp, ok := r.Pop(fk)
		if !ok {
			return out
		}
		if resp.RVal == nil {
			out = append(out, resp.K+":nil")
		} else {
			out = append(out, fmt.Sprintf("%s:%s", resp.K, *resp.RVal))
		}
	}
}
