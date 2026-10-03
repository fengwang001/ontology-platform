package srp

import (
	"strings"
	"testing"
)

func reasonOf(err error) Reason {
	if err == nil {
		return ""
	}
	return err.(*Error).Reason
}

// logOp 打印输入、输出与判定依据，保证每个拒绝原因可追溯。
func logOp(t *testing.T, op string, err error, why string, kv ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("INPUT: ")
	b.WriteString(op)
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteString(" " + kv[i] + "=" + kv[i+1])
	}
	if err != nil {
		b.WriteString(" | OUTPUT: REJECT reason=" + string(reasonOf(err)) + " (" + err.Error() + ")")
	} else {
		b.WriteString(" | OUTPUT: OK")
	}
	b.WriteString(" | JUDGE: " + why)
	t.Log(b.String())
}

func mustOp(t *testing.T, s *SRP, err error, op, why string, kv ...string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", op, err)
	}
	assertInvariant(t, s, op)
	logOp(t, op, nil, why, kv...)
}

func rejectOp(t *testing.T, s *SRP, err error, want Reason, op, why string, kv ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected rejection %s, got success", op, want)
	}
	if got := reasonOf(err); got != want {
		t.Fatalf("%s: expected reason %s, got %s (%v)", op, want, got, err)
	}
	assertInvariant(t, s, op)
	logOp(t, op, err, why, kv...)
}

func assertInvariant(t *testing.T, s *SRP, op string) {
	t.Helper()
	for id, r := range s.resources {
		held := 0
		for _, j := range s.stack {
			held += j.held[id]
		}
		if r.avail+held != r.n {
			t.Fatalf("after %s: resource %s avail=%d + held=%d != N=%d", op, id, r.avail, held, r.n)
		}
	}
	for i := 1; i < len(s.stack); i++ {
		pi := s.levelLocked(s.stack[i].task)
		prev := s.levelLocked(s.stack[i-1].task)
		if pi <= prev {
			t.Fatalf("after %s: stack pi not strictly increasing at %d: %d <= %d", op, i, pi, prev)
		}
	}
}

func stackIDs(s *SRP) []string {
	jobs := s.Stack()
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.ID
	}
	return out
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestWalkthrough 逐步复现题目给出的完整示例。
func TestWalkthrough(t *testing.T) {
	s := New()
	mustOp(t, s, s.DeclareResource("R", 3), "DeclareResource(R,3)", "N=3，avail 初值 3", "R", "3")
	mustOp(t, s, s.AddTask("A", 20, Mu{"R", 3}), "AddTask(A,D=20,muR=3)", "新增任务", "A", "D=20 muR=3")
	mustOp(t, s, s.AddTask("B", 10, Mu{"R", 1}), "AddTask(B,D=10,muR=1)", "新增任务", "B", "D=10 muR=1")
	mustOp(t, s, s.AddTask("C", 5, Mu{"R", 2}), "AddTask(C,D=5,muR=2)", "新增任务", "C", "D=5 muR=2")

	for _, c := range []struct {
		id string
		pi int
	}{{"A", 1}, {"B", 2}, {"C", 3}} {
		pi, err := s.Level(c.id)
		if err != nil || pi != c.pi {
			t.Fatalf("Level(%s)=%d,%v want %d", c.id, pi, err, c.pi)
		}
		t.Logf("QUERY Level(%s)=%d | JUDGE: D 从大到小 20,10,5 => π=1,2,3", c.id, pi)
	}
	if sc := s.SysCeil(); sc != 0 {
		t.Fatalf("SysCeil=%d want 0", sc)
	}
	t.Log("QUERY SysCeil()=0 | JUDGE: avail=3，没有 μ 严格大于 3")

	mustOp(t, s, s.Start("a1", "A"), "Start(a1,A)", "栈空且 π=1 > SysCeil=0，压栈", "a1", "A")
	mustOp(t, s, s.Acquire("a1", "R", 2), "Acquire(a1,R,2)", "栈顶且 0+2<=μ=3、avail 3>=2，avail=>1", "a1", "R:2")
	if c, _ := s.Ceil("R"); c != 3 {
		t.Fatalf("Ceil(R)=%d want 3", c)
	}
	t.Log("QUERY Ceil(R)=3 | JUDGE: avail=1，μ>1 的是 A(π1)、C(π3)，取 max=3")

	rejectOp(t, s, s.Start("c1", "C"), ReasonCeilingBlocked, "Start(c1,C)",
		"π=3 > 栈顶 π=1，但 π=3 不严格大于 SysCeil=3，只报天花板", "c1", "C")
	rejectOp(t, s, s.Start("b1", "B"), ReasonCeilingBlocked, "Start(b1,B)",
		"π=2 不严格大于 SysCeil=3", "b1", "B")

	mustOp(t, s, s.Release("a1", "R", 1), "Release(a1,R,1)", "归还 1，avail=2，天花板下降", "a1", "R:1")
	if sc := s.SysCeil(); sc != 1 {
		t.Fatalf("SysCeil=%d want 1", sc)
	}
	t.Log("QUERY SysCeil()=1 | JUDGE: avail=2，仅 A 的 3>2")
	mustOp(t, s, s.Start("b1", "B"), "Start(b1,B)", "π=2 > 栈顶 π=1 且 > SysCeil=1，压栈", "b1", "B")
	mustOp(t, s, s.Acquire("b1", "R", 1), "Acquire(b1,R,1)", "b1 为栈顶，1<=μ=1，avail=>1", "b1", "R:1")
	if sc := s.SysCeil(); sc != 3 {
		t.Fatalf("SysCeil=%d want 3", sc)
	}
	rejectOp(t, s, s.Start("c1", "C"), ReasonCeilingBlocked, "Start(c1,C)",
		"avail=1 使 SysCeil 回到 3，π=3 再被拦", "c1", "C")
	mustOp(t, s, s.Release("b1", "R", 1), "Release(b1,R,1)", "归还后 avail=2，SysCeil=1", "b1", "R:1")
	mustOp(t, s, s.Start("c1", "C"), "Start(c1,C)", "π=3 > 栈顶 π=2 且 > SysCeil=1，压栈", "c1", "C")

	if got := stackIDs(s); !eqStrings(got, []string{"a1", "b1", "c1"}) {
		t.Fatalf("Stack=%v want [a1 b1 c1]", got)
	}
	t.Log("QUERY Stack()=[a1 b1 c1] | JUDGE: 自栈底到栈顶严格按压栈次序")

	rejectOp(t, s, s.Finish("b1"), ReasonNotTopOfStack, "Finish(b1)",
		"b1 不是栈顶，c1 才是", "b1", "-")
	mustOp(t, s, s.Finish("c1"), "Finish(c1)", "栈顶且不持资源，弹栈", "c1", "-")
	mustOp(t, s, s.Finish("b1"), "Finish(b1)", "现在 b1 为栈顶，弹栈", "b1", "-")
	mustOp(t, s, s.Release("a1", "R", 1), "Release(a1,R,1)", "a1 归还剩余 1 单元", "a1", "R:1")
	mustOp(t, s, s.Finish("a1"), "Finish(a1)", "a1 不持资源，弹栈", "a1", "-")
	if n := s.shortageRejectionsLocked(); n != 0 {
		t.Fatalf("shortage counter=%d want 0", n)
	}
	t.Log("QUERY shortageRejections=0 | JUDGE: 遵守 μ 的合法序列下单元不足永不发生")
}

// TestCeilStrictInequality μ 恰等于 avail 不入天花板，大 1 才入。
func TestCeilStrictInequality(t *testing.T) {
	s := New()
	mustOp(t, s, s.DeclareResource("R", 3), "DeclareResource(R,3)", "N=3", "R", "3")
	mustOp(t, s, s.AddTask("T", 100, Mu{"R", 2}), "AddTask(T,muR=2)", "μ=2", "T", "muR=2")
	mustOp(t, s, s.Start("j", "T"), "Start(j,T)", "启动", "j", "T")
	mustOp(t, s, s.Acquire("j", "R", 1), "Acquire(j,R,1)", "avail=2", "j", "R:1")
	if c, _ := s.Ceil("R"); c != 0 {
		t.Fatalf("μ==avail 时 Ceil=%d want 0", c)
	}
	t.Log("QUERY Ceil(R)=0 | JUDGE: μ=2 不严格大于 avail=2")
	mustOp(t, s, s.Acquire("j", "R", 1), "Acquire(j,R,1)", "avail=1", "j", "R:1")
	if c, _ := s.Ceil("R"); c != 1 {
		t.Fatalf("μ=avail+1 时 Ceil=%d want 1", c)
	}
	t.Log("QUERY Ceil(R)=1 | JUDGE: μ=2 > avail=1，恰好大 1 即入天花板")
}

// TestSameDeadlineSameLevel 相同 D 同 π，不能互相抢占。
func TestSameDeadlineSameLevel(t *testing.T) {
	s := New()
	mustOp(t, s, s.DeclareResource("R", 1), "DeclareResource(R,1)", "N=1", "R", "1")
	mustOp(t, s, s.AddTask("X", 10), "AddTask(X,D=10)", "无 μ 声明，μR=0", "X", "D=10")
	mustOp(t, s, s.AddTask("Y", 10), "AddTask(Y,D=10)", "D 相同", "Y", "D=10")
	px, _ := s.Level("X")
	py, _ := s.Level("Y")
	if px != 1 || py != 1 {
		t.Fatalf("same D levels %d,%d want 1,1", px, py)
	}
	t.Log("QUERY Level(X)=Level(Y)=1 | JUDGE: D 相同 π 相同")
	mustOp(t, s, s.Start("x1", "X"), "Start(x1,X)", "压栈", "x1", "X")
	rejectOp(t, s, s.Start("y1", "Y"), ReasonPreemptionLevelLow, "Start(y1,Y)",
		"π=1 不严格大于栈顶 π=1", "y1", "Y")
}

// TestInsertMiddleDeadline 中间 D 插入后 π 顺延，栈次序仍严格递增。
func TestInsertMiddleDeadline(t *testing.T) {
	s := New()
	mustOp(t, s, s.DeclareResource("R", 4), "DeclareResource(R,4)", "N=4", "R", "4")
	mustOp(t, s, s.AddTask("Hi", 100), "AddTask(Hi,D=100)", "μR=0", "Hi", "D=100")
	mustOp(t, s, s.AddTask("Lo", 10), "AddTask(Lo,D=10)", "μR=0", "Lo", "D=10")
	mustOp(t, s, s.Start("lo1", "Lo"), "Start(lo1,Lo)", "Lo π=2 可空栈启动", "lo1", "Lo")
	mustOp(t, s, s.AddTask("Mid", 50), "AddTask(Mid,D=50)", "在 100 与 10 之间插入", "Mid", "D=50")
	want := map[string]int{"Hi": 1, "Mid": 2, "Lo": 3}
	for id, w := range want {
		if pi, _ := s.Level(id); pi != w {
			t.Fatalf("after insert Level(%s)=%d want %d", id, pi, w)
		}
	}
	t.Log("QUERY Level Hi=1 Mid=2 Lo=3 | JUDGE: 中间插入后其余 π 顺延，栈内 Lo π=3 仍然成立")
	rejectOp(t, s, s.Start("m1", "Mid"), ReasonPreemptionLevelLow, "Start(m1,Mid)",
		"Mid π=2 <= 栈顶 Lo 顺延后的 π=3", "m1", "Mid")
}

// TestMultiResourceMax 多资源天花板取最大。
func TestMultiResourceMax(t *testing.T) {
	s := New()
	mustOp(t, s, s.DeclareResource("R1", 1), "DeclareResource(R1,1)", "N=1", "R1", "1")
	mustOp(t, s, s.DeclareResource("R2", 1), "DeclareResource(R2,1)", "N=1", "R2", "1")
	mustOp(t, s, s.AddTask("Hi", 100, Mu{"R1", 1}), "AddTask(Hi,muR1=1)", "仅声明 R1", "Hi", "muR1=1")
	mustOp(t, s, s.AddTask("Lo", 1, Mu{"R1", 1}, Mu{"R2", 1}), "AddTask(Lo,muR1=1,muR2=1)", "同时声明 R1,R2", "Lo", "muR1=1 muR2=1")
	mustOp(t, s, s.Start("hi", "Hi"), "Start(hi,Hi)", "压栈", "hi", "Hi")
	mustOp(t, s, s.Acquire("hi", "R1", 1), "Acquire(hi,R1,1)", "R1 avail=0", "hi", "R1:1")
	c1, _ := s.Ceil("R1")
	c2, _ := s.Ceil("R2")
	if c1 != 2 || c2 != 0 {
		t.Fatalf("Ceil R1=%d R2=%d want 2,0", c1, c2)
	}
	if sc := s.SysCeil(); sc != 2 {
		t.Fatalf("SysCeil=%d want max(2,0)=2", sc)
	}
	t.Log("QUERY Ceil(R1)=2 Ceil(R2)=0 SysCeil=2 | JUDGE: 多资源取各资源天花板最大值")
}

// TestUnlistedResourceMuZero 未列出资源 μ=0：不能取得该资源。
func TestUnlistedResourceMuZero(t *testing.T) {
	s := New()
	mustOp(t, s, s.DeclareResource("R", 2), "DeclareResource(R,2)", "N=2", "R", "2")
	mustOp(t, s, s.AddTask("T", 10), "AddTask(T)", "未列出 R，μR=0", "T", "no-mu")
	mustOp(t, s, s.Start("j", "T"), "Start(j,T)", "压栈", "j", "T")
	rejectOp(t, s, s.Acquire("j", "R", 1), ReasonOverClaim, "Acquire(j,R,1)",
		"held 0 + 1 > μ=0，先报超出声明而非单元不足", "j", "R:1")
	a, _ := s.Avail("R")
	if a != 2 {
		t.Fatalf("avail=%d want 2，被拒操作不得改变状态", a)
	}
	t.Log("QUERY Avail(R)=2 | JUDGE: 拒绝不改变余量/栈/持有量/π")
}

// TestNonTopReleaseFinish 栈顶之外的 Release/Finish 被拒，Finish 还须无持有。
func TestNonTopReleaseFinish(t *testing.T) {
	s := New()
	mustOp(t, s, s.DeclareResource("R", 4), "DeclareResource(R,4)", "N=4", "R", "4")
	mustOp(t, s, s.AddTask("Bot", 100, Mu{"R", 2}), "AddTask(Bot,D=100)", "π=1，μ=2", "Bot", "muR=2")
	mustOp(t, s, s.AddTask("Top", 10, Mu{"R", 1}), "AddTask(Top,D=10)", "π=2，μ=1", "Top", "muR=1")
	mustOp(t, s, s.Start("bot", "Bot"), "Start(bot,Bot)", "空栈：π=1 > SysCeil=0", "bot", "Bot")
	mustOp(t, s, s.Acquire("bot", "R", 2), "Acquire(bot,R,2)", "avail=2，μ 不超 avail，天花板仍为 0", "bot", "R:2")
	mustOp(t, s, s.Start("top", "Top"), "Start(top,Top)", "π=2 > 栈顶 π=1 且 > SysCeil=0", "top", "Top")

	rejectOp(t, s, s.Release("bot", "R", 1), ReasonNotTopOfStack, "Release(bot,R,1)",
		"bot 在栈中但不是栈顶，即便持有也不许归还", "bot", "R:1")
	rejectOp(t, s, s.Acquire("bot", "R", 1), ReasonNotTopOfStack, "Acquire(bot,R,1)",
		"非栈顶同样不能取得资源", "bot", "R:1")
	rejectOp(t, s, s.Finish("bot"), ReasonNotTopOfStack, "Finish(bot)",
		"Finish 只允许栈顶，非栈顶先报 not_top_of_stack", "bot", "-")

	mustOp(t, s, s.Finish("top"), "Finish(top)", "top 为栈顶且不持资源，弹栈", "top", "-")
	rejectOp(t, s, s.Finish("bot"), ReasonStillHolding, "Finish(bot)",
		"bot 现在是栈顶但仍持有 R 2 单元", "bot", "-")
	mustOp(t, s, s.Release("bot", "R", 2), "Release(bot,R,2)", "归还全部，avail 恢复 4", "bot", "R:2")
	mustOp(t, s, s.Finish("bot"), "Finish(bot)", "不持资源，弹栈", "bot", "-")
}
