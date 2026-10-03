package deadline

import "testing"

func TestHeapPushPeekPop(t *testing.T) {
	h := NewHeap()
	h.Push("a", 5)
	h.Push("b", 2)
	h.Push("c", 8)
	if h.Peek().Req != "b" {
		t.Fatalf("peek=%s want b", h.Peek().Req)
	}
	h.Push("b", 1)
	if h.Peek().Req != "b" || h.Peek().Due != 1 {
		t.Fatalf("update peek=%+v", h.Peek())
	}
	order := []string{"b", "a", "c"}
	for _, want := range order {
		it := h.Pop()
		if it == nil || it.Req != want {
			t.Fatalf("pop=%v want %s", it, want)
		}
	}
	if h.Pop() != nil {
		t.Fatal("empty pop must be nil")
	}
}

func TestHeapTenThousandUnique(t *testing.T) {
	h := NewHeap()
	for i := 0; i < 10_000; i++ {
		h.Push("k"+itoa(i), 1000)
	}
	if h.Len() != 10_000 {
		t.Fatalf("len=%d want 10000", h.Len())
	}
	for i := 0; i < 10_000; i++ {
		if h.Pop() == nil {
			t.Fatalf("pop %d nil", i)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestHeapRemove(t *testing.T) {
	h := NewHeap()
	h.Push("a", 1)
	h.Push("b", 2)
	h.Push("c", 3)
	h.Remove("b")
	if h.Len() != 2 {
		t.Fatalf("len=%d", h.Len())
	}
	if h.Pop().Req != "a" || h.Pop().Req != "c" {
		t.Fatal("remove wrong")
	}
}

// 模拟一次 Drain：r 连升（多次 pop/push），q 终局（pop），随后整体回滚。
func TestHeapRestoreAfterDrain(t *testing.T) {
	h := NewHeap()
	h.Push("q", 5)
	h.Push("r", 10)
	var popped []*Item
	h.BeginDrain()
	// pop q at5 -> expired, not repushed.
	popped = append(popped, h.Pop())
	h.Examine()
	// pop r at10 -> escalate, repush at20.
	it := h.Pop()
	popped = append(popped, it)
	h.Push("r", 20)
	// pop r at20 -> escalate repush at30.
	it = h.Pop()
	popped = append(popped, it)
	h.Push("r", 30)
	h.Examine()
	if h.Len() != 1 {
		t.Fatalf("mid drain len=%d", h.Len())
	}
	h.Restore(popped, map[string]int64{"q": 5, "r": 10})
	if h.Len() != 2 {
		t.Fatalf("restored len=%d want 2", h.Len())
	}
	if top := h.Peek(); top.Req != "q" || top.Due != 5 {
		t.Fatalf("top=%+v want q@5", top)
	}
	h.Pop()
	if top := h.Peek(); top.Req != "r" {
		t.Fatalf("top=%+v want r", top)
	}
}

// r 终局：pop 后不再 repush；回滚后应回到堆中且 Due 为旧值。
func TestHeapRestoreExpired(t *testing.T) {
	h := NewHeap()
	h.Push("r", 40)
	h.Push("q0", 1000)
	var popped []*Item
	popped = append(popped, h.Pop()) // r expires at40, not repushed
	h.Restore(popped, map[string]int64{"r": 40})
	if h.Len() != 2 {
		t.Fatalf("len=%d want 2", h.Len())
	}
	if top := h.Peek(); top.Req != "r" || top.Due != 40 {
		t.Fatalf("top=%+v want r@40", top)
	}
}

// 精确复现 approval 序列：r 经 40 终局的虚拟 Drain 后回滚（Due 恢复 10），
// 随后下一次成功操作 Drain 到 990：r 在 10 被 pop 终局，再 Push q0@1000。
func TestHeapApprovalSequence(t *testing.T) {
	h := NewHeap()
	h.Push("r", 10)
	// 第一次 Status(40)：r 只升级/终局一次（最后候选到期），pop 不 repush。
	var popped []*Item
	h.BeginDrain()
	// 模拟 r 有 4 个候选：pop r@10 push@20, pop@20 push@30, pop@30 push@40, pop@40 expired。
	for _, due := range []int64{10, 20, 30, 40} {
		it := h.Pop()
		if it.Due != due {
			t.Fatalf("pop due=%d want %d", it.Due, due)
		}
		popped = append(popped, it)
		if due != 40 {
			h.Push("r", due+10)
		}
		h.Examine()
	}
	if h.Len() != 0 {
		t.Fatalf("drained len=%d", h.Len())
	}
	// Status 失败语义回滚（这里是只读 Status，总是回滚）。
	h.Restore(popped, map[string]int64{"r": 10})
	if h.Len() != 1 || h.Peek().Req != "r" || h.Peek().Due != 10 {
		t.Fatalf("restored: len=%d top=%+v", h.Len(), h.Peek())
	}
	// 成功 Submit q0@990：先 expire 到 990，r@10 被 pop 终局（不 repush），
	// 然后 Push q0 到期 1000。
	h.BeginDrain()
	it := h.Pop()
	h.Examine()
	if it.Req != "r" || it.Due != 10 {
		t.Fatalf("second drain pop=%+v", it)
	}
	if h.Peek() != nil {
		h.Examine()
	}
	h.Push("q0", 1000)
	if h.Len() != 1 || h.Peek().Req != "q0" {
		t.Fatalf("final len=%d top=%+v", h.Len(), h.Peek())
	}
}
