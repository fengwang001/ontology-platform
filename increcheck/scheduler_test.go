package increcheck

import (
	"reflect"
	"testing"
)

func assertStrings(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func TestImplDependencyDoesNotPropagate(t *testing.T) {
	s := NewScheduler()
	mustAdd(t, s, "a", Declaration{SigText: "sa", ImplText: "ia"})
	mustAdd(t, s, "b", Declaration{SigText: "sb", ImplText: "$a"})
	mustAdd(t, s, "c", Declaration{SigText: "sc", ImplText: "$b"})

	resetOutcome(s)
	if err := s.EditSig("a", "sa2"); err != nil {
		t.Fatal(err)
	}
	oc := s.LastOutcome()
	// 签名依赖链上没有签名依赖 a/b/c：只有实现读取签名。
	assertStrings(t, "changed", oc.ChangedSigs, []string{"a"})
	assertStrings(t, "rechecked sigs", oc.RecheckedSigs, []string{"a"})
	// a 自身的实现检查随签名编辑一并重检；b 的实现直接依赖 a 而重检；
	// c 的实现只依赖 b 的签名，b 签名未变 -> c 沿用。
	assertStrings(t, "rechecked impls", oc.RecheckedImpls, []string{"a", "b"})
	assertStrings(t, "reused impls in edit", oc.ReusedImpls, nil)
	// c 的实现只依赖 b 的签名，b 版本未变：随后查询 c 时依据逐一直等，
	// 返回旧结果并计入累计沿用，不触发任何检查。
	callsBefore := s.checker.Calls
	ce := s.ImplResult("c")
	if ce.Result.Err != ErrNone {
		t.Fatalf("c impl should be reusable, got %+v", ce.Result)
	}
	if s.checker.Calls != callsBefore {
		t.Fatalf("c must be reused without recheck, calls=%d", s.checker.Calls-callsBefore)
	}
	if s.Stats().Reused == 0 {
		t.Fatal("reuse must be counted in stats")
	}
}

func TestSigDependencyPropagates(t *testing.T) {
	s := NewScheduler()
	mustAdd(t, s, "a", Declaration{SigText: "sa"})
	mustAdd(t, s, "b", Declaration{SigText: "$a"})
	mustAdd(t, s, "c", Declaration{SigText: "$b"})

	resetOutcome(s)
	// 删除 a：存在性翻转，b、c 沿签名依赖逐级变为未定义并递增版本。
	if err := s.EditSig("a", "sa2"); err != nil {
		t.Fatal(err)
	}
	// 先确认纯文本变化在 b 处早停（结果身份相同）。
	oc := s.LastOutcome()
	assertStrings(t, "text-edit changed", oc.ChangedSigs, []string{"a"})
	assertStrings(t, "text-edit early", oc.StoppedEarly, []string{"b"})
	assertStrings(t, "text-edit saved", oc.SavedByEarlyStop, []string{"c"})
	resetOutcome(s)
	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	oc = s.LastOutcome()
	assertStrings(t, "delete changed", oc.ChangedSigs, []string{"a", "b", "c"})
}

func TestEarlyStopTruncatesPropagation(t *testing.T) {
	s := NewScheduler()
	mustAdd(t, s, "x", Declaration{SigText: "sx"})
	mustAdd(t, s, "y", Declaration{SigText: "sy"})
	mustAdd(t, s, "a", Declaration{SigText: "$x"})
	mustAdd(t, s, "b", Declaration{SigText: "$a"})

	// 先经历一次存在性翻转建立 a 正常、b 正常的状态。
	if err := s.Delete("x"); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("x", Declaration{SigText: "sx"}); err != nil {
		t.Fatal(err)
	}
	resetOutcome(s)
	// x 仅改签名文本：x 变化；a 重检后结果身份逐字段相同（文本 "$x"
	// 未变、x 仍存在、无错误）-> a 早停；b 不被触及，不计任何动作。
	if err := s.EditSig("x", "sx2"); err != nil {
		t.Fatal(err)
	}
	oc := s.LastOutcome()
	assertStrings(t, "changed", oc.ChangedSigs, []string{"x"})
	assertStrings(t, "early", oc.StoppedEarly, []string{"a"})
	assertStrings(t, "saved", oc.SavedByEarlyStop, []string{"b"})
	if contains(oc.RecheckedSigs, "b") {
		t.Fatalf("b must not be rechecked after early stop: %v", oc.RecheckedSigs)
	}
	if s.Stats().StoppedEarly == 0 {
		t.Fatal("stats StoppedEarly must record the early stop")
	}
}

func TestCycleRecheckedAsWholeAndGroupError(t *testing.T) {
	s := NewScheduler()
	mustAdd(t, s, "a", Declaration{SigText: "$b"})
	mustAdd(t, s, "b", Declaration{SigText: "$a"})
	mustAdd(t, s, "c", Declaration{SigText: "$a"})

	resetOutcome(s)
	// 删除 a：SCC{a,b} 整组重检；a absent、b 未定义；c 也未定义。
	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	oc := s.LastOutcome()
	if !sameSet(oc.ChangedSigs, []string{"a", "b", "c"}) {
		t.Fatalf("changed = %v", oc.ChangedSigs)
	}
	if e := s.SigResult("b"); e.Result.Err != ErrUndefined {
		t.Fatalf("b should be undefined, got %+v", e.Result)
	}
	if e := s.SigResult("c"); e.Result.Err != ErrUndefined {
		t.Fatalf("c downstream should see group as changed/undefined, got %+v", e.Result)
	}
}

func TestDeleteThenReaddCausesSecondInvalidation(t *testing.T) {
	s := NewScheduler()
	mustAdd(t, s, "x", Declaration{SigText: "sx"})
	mustAdd(t, s, "a", Declaration{SigText: "$x"})

	v1 := s.SigResult("a").Basis["x"]
	if err := s.Delete("x"); err != nil {
		t.Fatal(err)
	}
	if e := s.SigResult("a"); e.Result.Err != ErrUndefined {
		t.Fatalf("a undefined expected, got %+v", e.Result)
	}
	if err := s.Add("x", Declaration{SigText: "sx"}); err != nil {
		t.Fatal(err)
	}
	e := s.SigResult("a")
	if e.Result.Err != ErrNone {
		t.Fatalf("a should recover, got %+v", e.Result)
	}
	if e.Basis["x"] <= v1 {
		t.Fatalf("x version must advance on readd: old=%d new=%d", v1, e.Basis["x"])
	}
}

func TestNoOpHasZeroSideEffects(t *testing.T) {
	s := NewScheduler()
	mustAdd(t, s, "a", Declaration{SigText: "sa", ImplText: "ia"})
	v := s.reg.sigVersion("a")
	if err := s.EditSig("a", "sa"); !isErrCode(err, ErrNoOp) {
		t.Fatalf("want ErrNoOp, got %v", err)
	}
	if err := s.EditImpl("a", "ia"); !isErrCode(err, ErrNoOp) {
		t.Fatalf("want ErrNoOp, got %v", err)
	}
	if s.reg.sigVersion("a") != v {
		t.Fatal("no-op must not allocate versions")
	}
	if s.Stats().Edits != 1 || s.Stats().NoOps != 2 {
		t.Fatalf("stats wrong: %+v", s.Stats())
	}
}

func TestNotFoundAndExistsErrors(t *testing.T) {
	s := NewScheduler()
	if err := s.EditSig("nope", "x"); !isErrCode(err, ErrDeclNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := s.Delete("nope"); !isErrCode(err, ErrDeclNotFound) {
		t.Fatalf("got %v", err)
	}
	mustAdd(t, s, "a", Declaration{SigText: "sa"})
	if err := s.Add("a", Declaration{SigText: "sb"}); !isErrCode(err, ErrDeclExists) {
		t.Fatalf("got %v", err)
	}
}

func TestBasisMismatchRejectsReuse(t *testing.T) {
	c := NewCache()
	reg := NewRegistry()
	reg.add("x", Declaration{SigText: "sx"})
	reg.bump("x") // v1
	c.putSig("a", SigEntry{
		Result: SigResult{Present: true, Text: "$x"},
		Basis:  Basis{"x": 1},
	})
	if ok, _ := c.validSig("a", reg); !ok {
		t.Fatal("basis should be valid at v1")
	}
	reg.editSig("x", "sx2")
	reg.bump("x") // v2: 模拟实际变化
	if ok, stale := c.validSig("a", reg); ok || stale != "x" {
		t.Fatalf("reuse must be rejected, stale=%v", stale)
	}
}

func mustAdd(t *testing.T, s *Scheduler, id string, d Declaration) {
	t.Helper()
	if err := s.Add(id, d); err != nil {
		t.Fatalf("add %s: %v", id, err)
	}
}

func resetOutcome(s *Scheduler) { s.last = nil }

func sameSet(xs, ys []string) bool {
	if len(xs) != len(ys) {
		return false
	}
	for _, x := range xs {
		if !contains(ys, x) {
			return false
		}
	}
	return true
}
