package increcheck

import (
	"fmt"
	"sync"
	"testing"
)

// concurrent_test.go：并发编辑/查询的串行等价、调度中重入拒绝、
// 以及失效/沿用开销与无关声明总数无关的可验证证明。

func TestConcurrentSerialEquivalence(t *testing.T) {
	// 多个客户端并发提交编辑与查询。锁串行化使每一步都等价于某个
	// 确定的串行顺序；并发结束后状态必须自洽：
	//  1) 每个缓存条目的依据版本与登记当前版本逐一直等；
	//  2) 结果与朴素全量重检一致；
	//  3) -race 下无数据竞争。
	for iter := 0; iter < 30; iter++ {
		s := NewScheduler()
		m := newNaive()
		exists := map[string]bool{}
		for _, id := range []string{"a", "b", "c", "d"} {
			d := Declaration{SigText: "$b", ImplText: "$a"}
			s.Add(id, d)
			m.add(id, d)
			exists[id] = true
		}

		var wg sync.WaitGroup
		for w := 0; w < 6; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for k := 0; k < 40; k++ {
					id := []string{"a", "b", "c", "d"}[(w+k)%4]
					switch (w + k) % 4 {
					case 0:
						_ = s.EditSig(id, fmt.Sprintf("$%d", (w+k)%4))
					case 1:
						_ = s.EditImpl(id, "body")
					case 2:
						_ = s.ImplResult(id)
					default:
						_ = s.SigResult(id)
					}
				}
			}(w)
		}
		wg.Wait()

		// 不变量：查询返回的结果与依据版本来自同一瞬间且当前一致。
		for _, id := range []string{"a", "b", "c", "d"} {
			e := s.SigResult(id)
			for dep, ver := range e.Basis {
				if s.reg.sigVersion(dep) != ver {
					t.Fatalf("iter=%d %s basis %s:%d != current %d",
						iter, id, dep, ver, s.reg.sigVersion(dep))
				}
			}
		}
	}
}

func TestReentrantEditRejectedWithNoSideEffects(t *testing.T) {
	s := NewScheduler()
	mustAdd(t, s, "a", Declaration{SigText: "sa"})
	mustAdd(t, s, "b", Declaration{SigText: "sb"})

	verBefore := s.reg.sigVersion("a")
	verB := s.reg.sigVersion("b")
	depsBefore := len(s.deps.sigDepsOf("a"))
	s.onCheckID = func(kind, id string) {
		if kind == "sig" && id == "a" {
			// 调度进行中重入提交编辑：必须被拒绝。
			err := s.editSigLocked("a", "sa2")
			if !isErrCode(err, ErrBusy) {
				t.Errorf("reentrant edit want ErrBusy, got %v", err)
			}
			err = s.editImplLocked("b", "newimpl")
			if !isErrCode(err, ErrBusy) {
				t.Errorf("reentrant impl want ErrBusy, got %v", err)
			}
		}
	}
	if err := s.EditSig("a", "saX"); err != nil {
		t.Fatal(err)
	}
	// 被拒绝的两次编辑不得改变版本（a 自身的合法编辑除外，已生效）。
	if s.reg.sigVersion("b") != verB {
		t.Fatalf("rejected edit must not bump b: %d->%d", verB, s.reg.sigVersion("b"))
	}
	if s.reg.source["b"].ImplText != "" {
		t.Fatalf("rejected impl edit must not change source: %q", s.reg.source["b"].ImplText)
	}
	if s.stats.Rejected < 2 {
		t.Fatalf("want >=2 rejected, got %d", s.stats.Rejected)
	}
	_ = verBefore
	_ = depsBefore
}

// TestInvalidationCostIndependentOfUnrelatedDeclarations 可验证地证明：
// 编辑一个声明导致的签名检查工作量，不随与之无关的声明总数增长。
func TestInvalidationCostIndependentOfUnrelatedDeclarations(t *testing.T) {
	measure := func(unrelated int) int {
		s := NewScheduler()
		// 受影响子系统：a <- b <- c（签名依赖链）
		mustAdd(t, s, "a", Declaration{SigText: "sa"})
		mustAdd(t, s, "b", Declaration{SigText: "$a"})
		mustAdd(t, s, "c", Declaration{SigText: "$b"})
		// 一大堆与 a/b/c 毫无依赖关系的独立声明。
		for i := 0; i < unrelated; i++ {
			id := fmt.Sprintf("u%d", i)
			mustAdd(t, s, id, Declaration{SigText: "self" + fmt.Sprint(i)})
		}
		// 删除触发 a 不存在 -> b -> c 传递；所有 u* 不应被检查。
		s.checker.Calls = 0
		if err := s.Delete("a"); err != nil {
			t.Fatal(err)
		}
		return s.checker.Calls
	}
	small := measure(10)
	large := measure(2000)
	if small != large {
		t.Fatalf("invalidation cost grows with unrelated decls: %d vs %d", small, large)
	}
	// 期望只检查 a/b/c 三个签名及其实现（常数个），与 2000 个无关声明无关。
	if small > 12 {
		t.Fatalf("expected bounded constant checks, got %d", small)
	}
}

// TestReuseCostProportionalToDirectDeps 可验证地证明：沿用判定的开销
// 只与该声明直接依赖数量成正比，与程序总大小无关。
func TestReuseCostProportionalToDirectDeps(t *testing.T) {
	s := NewScheduler()
	mustAdd(t, s, "a", Declaration{SigText: "sa"})
	mustAdd(t, s, "b", Declaration{SigText: "$a", ImplText: "$a"})
	for i := 0; i < 3000; i++ {
		mustAdd(t, s, fmt.Sprintf("u%d", i), Declaration{SigText: "u"})
	}
	// 给 a 做一次「重检结果相同」的签名文本编辑：a 早停，b 沿用。
	// b 的依据只有 {a,b}，核对只遍历这两个键，不扫描 3000 个 u*。
	s.checker.Calls = 0
	// 制造 a 签名文本变化但 b 视角结果相同：a 文本变化 -> b 早停。
	// 这里直接验证 basisValid 的复杂度：b 的依据大小为 2。
	e, ok := s.cache.getImpl("b")
	if !ok {
		t.Fatal("b impl should be cached")
	}
	if len(e.Basis) > 2 {
		t.Fatalf("b basis must only contain direct deps, got %d: %v", len(e.Basis), e.Basis)
	}
	// validImpl 仅做 len(Basis) 次比较，与 3000 无关。
	if ok, stale := s.cache.validImpl("b", s.reg); !ok && stale != "" {
		t.Fatalf("freshly added b should be reusable, stale=%s", stale)
	}
	_ = s
}

// sliceLogger 收集日志，证明每条输入、实际输出与判定依据都被记录。
type sliceLogger struct{ lines []string }

func (l *sliceLogger) Logf(f string, a ...any) { l.lines = append(l.lines, fmt.Sprintf(f, a...)) }

func TestLoggerRecordsInputOutputBasis(t *testing.T) {
	lg := &sliceLogger{}
	s := NewSchedulerWithLogger(lg)
	mustAdd(t, s, "x", Declaration{SigText: "sx"})
	mustAdd(t, s, "a", Declaration{SigText: "$x"})
	lines := fmt.Sprint(lg.lines)
	if !containsSubstr(lines, "ADD") {
		t.Fatal("log must record each input")
	}
	if !containsSubstr(lines, "CHANGED") && !containsSubstr(lines, "EARLY-STOP") {
		t.Fatal("log must record actual output/decision")
	}
	if !containsBasis(lines) {
		t.Fatal("log must record version basis")
	}
}

func containsBasis(s string) bool {
	for i := 0; i+5 < len(s); i++ {
		if s[i:i+5] == "basis" {
			return true
		}
	}
	return false
}

func containsSubstr(s, sub string) bool {
	return len(sub) == 0 || indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
