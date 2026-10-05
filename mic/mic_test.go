package mic

import (
	"fmt"
	"testing"
)

// 从队列中部移除一人，触碰的队列节点数不超过 3，与队列长度无关。
func TestRemoveMiddleTouched(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("queue=%d", n), func(t *testing.T) {
			s := New(1)
			s.Take("on-mic") // 占住唯一麦位，其余入队
			users := make([]string, n)
			for i := range users {
				users[i] = fmt.Sprintf("u%d", i)
				s.Take(users[i])
			}
			if s.qLen != n {
				t.Fatalf("queue len = %d, want %d", s.qLen, n)
			}
			s.touched = 0
			s.Drop(users[n/2]) // 从队列中部移除
			if s.touched > 3 {
				t.Fatalf("touched = %d, want <= 3 (queue len %d)", s.touched, n)
			}
			if s.qLen != n-1 || s.InQueue(users[n/2]) {
				t.Fatalf("remove failed: qLen=%d stillInQueue=%v", s.qLen, s.InQueue(users[n/2]))
			}
		})
	}
}

// 一次补麦检查的队列项数不超过上麦人数加被跳过的禁言者数加 1。
func TestFillCheckedBound(t *testing.T) {
	s := New(3)
	s.Take("m0")
	s.Take("m1")
	s.Take("m2") // 三个麦位占满
	queued := []string{"q0", "q1", "q2", "q3", "q4", "q5"}
	for _, u := range queued {
		s.Take(u)
	}
	s.ForceDropMic("m0")
	s.ForceDropMic("m2") // 空出 0 号与 2 号麦位
	mutedSet := map[string]bool{"q0": true, "q2": true, "q4": true}
	s.checked = 0
	s.Fill(func(u string) bool { return mutedSet[u] })

	fills, skipped := 0, 0
	for _, u := range queued {
		switch {
		case s.OnMic(u):
			fills++
		case mutedSet[u] && s.InQueue(u):
			skipped++
		}
	}
	if fills != 2 || skipped != 3 {
		t.Fatalf("fills=%d skipped=%d, want 2 and 3", fills, skipped)
	}
	if s.checked > fills+skipped+1 {
		t.Fatalf("checked = %d, want <= fills+skipped+1 = %d", s.checked, fills+skipped+1)
	}
	// 上麦的应是 q1、q3，分别在 0、2 号麦位；禁言者保持原相对顺序。
	if s.slots[0] != "q1" || s.slots[2] != "q3" || s.slots[1] != "m1" {
		t.Fatalf("slots = %v", s.slots)
	}
	if got := s.Queue(); len(got) != 4 || got[0] != "q0" || got[1] != "q2" || got[2] != "q4" || got[3] != "q5" {
		t.Fatalf("queue = %v", got)
	}
}

// Clone 后互不影响，供拒绝回滚使用。
func TestCloneIndependent(t *testing.T) {
	s := New(2)
	s.Take("a")
	s.Take("b")
	s.Take("c")
	c := s.Clone()
	c.Drop("a")
	c.Drop("c")
	if !s.OnMic("a") || !s.InQueue("c") {
		t.Fatalf("clone mutated source: slots=%v queue=%v", s.slots, s.Queue())
	}
	if c.OnMic("a") || c.InQueue("c") {
		t.Fatalf("clone not applied: slots=%v queue=%v", c.slots, c.Queue())
	}
}
