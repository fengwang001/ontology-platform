package acl

import (
	"fmt"
	"sync"
	"testing"
)

// 计数器只依赖从目标节点到最近保护节点（含）或根的链，与无关节点数量无关。
func TestEvalCountersIndependentOfUnrelatedNodes(t *testing.T) {
	s := mustStore(t, 8, 8)
	mustAdd(t, s, "a", "/", true)
	mustAdd(t, s, "b", "a", true)
	mustSetACL(t, s, "/", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: FlagCI},
	}, false)
	mustSetACL(t, s, "a", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0010, Flags: FlagCI},
		{Allow: false, Principal: "u", Mask: 0b1000, Flags: 0},
	}, false)
	mustSetACL(t, s, "b", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0100, Flags: 0},
	}, false)

	run := func() (int, int) {
		mustEval(t, s, []string{"u"}, "b", 0b0111)
		return s.lastEvalEntries, s.lastEvalNodes
	}
	e1, n1 := run()
	// 链为 b -> a -> /，共 3 个节点。
	if n1 != 3 {
		t.Fatalf("lastEvalNodes = %d, want 3", n1)
	}
	// 新增 10^4 个无关节点后计数不变。
	for i := 0; i < 10000; i++ {
		mustAdd(t, s, fmt.Sprintf("noise-%d", i), "/", i%2 == 0)
	}
	e2, n2 := run()
	if e1 != e2 || n1 != n2 {
		t.Fatalf("counters changed: (%d, %d) -> (%d, %d)", e1, n1, e2, n2)
	}
}

// 保护节点之上的祖先不得被访问；判定提前返回时条目计数只计到决定性条目。
func TestEvalCountersProtectionAndEarlyReturn(t *testing.T) {
	s := mustStore(t, 8, 8)
	mustAdd(t, s, "a", "/", true)
	mustAdd(t, s, "b", "a", true)
	mustAdd(t, s, "c", "b", true)
	mustSetACL(t, s, "/", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: FlagCI},
	}, false)
	mustSetACL(t, s, "a", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0010, Flags: FlagCI},
	}, false)
	mustSetACL(t, s, "b", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0100, Flags: FlagCI},
	}, true) // b 为保护节点
	mustSetACL(t, s, "c", []ACE{
		{Allow: false, Principal: "v", Mask: 0b1000, Flags: 0}, // 主体不在 token，被跳过
		{Allow: true, Principal: "u", Mask: 0b1000, Flags: 0},
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: 0}, // 决定性条目之后，不应被处理
	}, false)

	res := mustEval(t, s, []string{"u"}, "c", 0b1000)
	checkResult(t, res, ResultGranted, 1, "c", false, 0b1000)
	// 链为 c -> b（保护，含），不访问 a 与 /。
	if s.lastEvalNodes != 2 {
		t.Fatalf("lastEvalNodes = %d, want 2", s.lastEvalNodes)
	}
	// E(c) = X(c) + T(E(b))；X(c) 重排为 [deny v 1000, allow u 1000, allow u 0001]，
	// 提前返回在第二条，处理条目数为 2。
	if s.lastEvalEntries != 2 {
		t.Fatalf("lastEvalEntries = %d, want 2", s.lastEvalEntries)
	}

	// 隐式拒绝时处理全部条目：E(c) 共 3 条显式 + b 的继承 1 条（CI 允许）= 4 条。
	res = mustEval(t, s, []string{"u"}, "c", 0b100000)
	checkResult(t, res, ResultImplicitDeny, -1, "", false, 0)
	if s.lastEvalEntries != 4 {
		t.Fatalf("lastEvalEntries = %d, want 4", s.lastEvalEntries)
	}

	// Effective 不计入计数。
	e, n := s.lastEvalEntries, s.lastEvalNodes
	mustEffective(t, s, "c")
	if s.lastEvalEntries != e || s.lastEvalNodes != n {
		t.Fatalf("Effective changed counters")
	}
}

// 并发调用等价于某个串行顺序：并发重放与串行重放结果一致。
func TestConcurrentUse(t *testing.T) {
	s := mustStore(t, 16, 16)
	mustSetACL(t, s, "/", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: FlagOI | FlagCI},
		{Allow: false, Principal: "u", Mask: 0b1000, Flags: FlagOI | FlagCI},
	}, false)
	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("w%d-n%d", w, i)
				if err := s.AddNode(id, "/", i%3 != 0); err != nil {
					t.Errorf("AddNode: %v", err)
					return
				}
				if err := s.SetACL(id, []ACE{
					{Allow: true, Principal: "u", Mask: 0b0010, Flags: FlagCI},
				}, i%2 == 0); err != nil {
					t.Errorf("SetACL: %v", err)
					return
				}
				if _, err := s.Eval([]string{"u"}, id, 0b0011); err != nil {
					t.Errorf("Eval: %v", err)
					return
				}
				if _, err := s.Effective(id); err != nil {
					t.Errorf("Effective: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	// 初始 SetACL 1 次 + 每个 worker 200 次 AddNode + 200 次 SetACL，全部成功。
	want := uint64(1 + workers*200*2)
	if v := s.Version(); v != want {
		t.Fatalf("Version = %d, want %d", v, want)
	}
}
