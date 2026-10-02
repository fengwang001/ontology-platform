package infer

import (
	"testing"
)

// 变量与变量合一：左绑右，右侧层级取两者较小者。
func TestUnifyVarVarBindsLeftToRight(t *testing.T) {
	s := mustSession(t, 10)
	must(t, s.Enter()) // L=1
	v1 := mustNewVar(t, s)
	must(t, s.Enter()) // L=2
	v2 := mustNewVar(t, s)
	must(t, s.Unify(v1, v2))
	if got := resolveStr(t, s, v1); got != "Var(2)" {
		t.Fatalf("Var(1) should be bound to Var(2), resolved to %s", got)
	}
	if l := mustLevel(t, s, 2); l != 1 {
		t.Fatalf("Var(2) level = %d, want 1 (min of 1 and 2)", l)
	}
	if l := mustLevel(t, s, 1); l != 1 {
		t.Fatalf("Level(1) should follow the chain to Var(2) with level 1, got %d", l)
	}
}

// 变量绑定到构造子：只降层级更高的变量，不升层级更低的变量。
func TestUnifyVarConLowersOnlyHigherLevels(t *testing.T) {
	s := mustSession(t, 10)
	v1 := mustNewVar(t, s) // level 0
	must(t, s.Enter())     // L=1
	v2 := mustNewVar(t, s) // level 1
	must(t, s.Enter())     // L=2
	v3 := mustNewVar(t, s) // level 2
	must(t, s.Leave())     // L=1
	must(t, s.Leave())     // L=0
	must(t, s.Unify(v1, Con("Fn", v2, v3)))
	if l := mustLevel(t, s, 2); l != 0 {
		t.Fatalf("Var(2) level = %d, want 0", l)
	}
	if l := mustLevel(t, s, 3); l != 0 {
		t.Fatalf("Var(3) level = %d, want 0", l)
	}

	// 左侧变量层级为 1，右侧内层级更低的变量（0）不得被抬高。
	s2 := mustSession(t, 10)
	must(t, s2.Enter())    // L=1
	a := mustNewVar(t, s2) // level 1
	must(t, s2.Leave())    // L=0
	b := mustNewVar(t, s2) // level 0
	must(t, s2.Unify(a, Con("List", b)))
	if l := mustLevel(t, s2, 2); l != 0 {
		t.Fatalf("Var(2) level = %d, want 0 (must not be raised)", l)
	}
}

// 右侧变量作为被绑定方：左为构造子应用时，右侧变量绑定到左侧。
func TestUnifyRightVarIsBound(t *testing.T) {
	s := mustSession(t, 10)
	v1 := mustNewVar(t, s)
	v2 := mustNewVar(t, s)
	must(t, s.Unify(Con("List", v1), v2))
	if got := resolveStr(t, s, v2); got != "List(Var(1))" {
		t.Fatalf("Var(2) should be bound to List(Var(1)), got %s", got)
	}
	if _, err := s.Level(2); err == nil {
		t.Fatal("Level(2) should fail: Var(2) is bound to a constructor")
	} else {
		wantErr(t, err, ErrBound)
	}
}

// 同一变量自合一成功且不产生绑定。
func TestUnifySelfSucceeds(t *testing.T) {
	s := mustSession(t, 10)
	v1 := mustNewVar(t, s)
	must(t, s.Unify(v1, v1))
	if got := resolveStr(t, s, v1); got != "Var(1)" {
		t.Fatalf("Var(1) should stay unbound, got %s", got)
	}
}

// 出现检查经由已有绑定链也能发现：Var1 绑到 Var2 后，
// Unify(Var2, List(Var1)) 必须失败。
func TestOccursCheckThroughBindingChain(t *testing.T) {
	s := mustSession(t, 10)
	v1 := mustNewVar(t, s)
	v2 := mustNewVar(t, s)
	must(t, s.Unify(v1, v2))
	before := dumpSession(s)
	wantErr(t, s.Unify(v2, Con("List", v1)), ErrOccurs)
	if after := dumpSession(s); after != before {
		t.Fatalf("state changed after rejected Unify:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// 构造子不匹配与出现检查失败后，绑定与层级一并完全回滚。
func TestUnifyFailureRollsBackLevelsAndBindings(t *testing.T) {
	// 规格中的原子性例：先绑定并降层级，随后构造子不匹配。
	s := mustSession(t, 10)
	v1 := mustNewVar(t, s) // level 0
	must(t, s.Enter())
	v2 := mustNewVar(t, s) // level 1
	err := s.Unify(Con("Pair", v1, Con("Int")), Con("Pair", Con("List", v2), Con("Bool")))
	wantErr(t, err, ErrMismatch)
	if got := resolveStr(t, s, v1); got != "Var(1)" {
		t.Fatalf("Var(1) should be unbound after rollback, got %s", got)
	}
	if l := mustLevel(t, s, 2); l != 1 {
		t.Fatalf("Var(2) level = %d after rollback, want 1", l)
	}

	// 出现检查失败同样回滚层级：第一个参数先降 Var(2) 的层级，
	// 第二个参数触发出现检查失败。
	s2 := mustSession(t, 10)
	a := mustNewVar(t, s2) // level 0
	must(t, s2.Enter())
	b := mustNewVar(t, s2) // level 1
	err = s2.Unify(
		Con("Pair", a, a),
		Con("Pair", Con("List", b), Con("List", a)),
	)
	wantErr(t, err, ErrOccurs)
	if got := resolveStr(t, s2, a); got != "Var(1)" {
		t.Fatalf("Var(1) should be unbound after rollback, got %s", got)
	}
	if l := mustLevel(t, s2, 2); l != 1 {
		t.Fatalf("Var(2) level = %d after rollback, want 1", l)
	}
}

// 参数从左到右合一：先失败的子项决定原因。
// 若先处理右参数会得到出现检查失败；正确顺序应报构造子不匹配。
func TestUnifyArgsLeftToRightDeterminesError(t *testing.T) {
	s := mustSession(t, 10)
	v1 := mustNewVar(t, s)
	err := s.Unify(
		Con("Pair", Con("Int"), v1),
		Con("Pair", Con("Bool"), Con("List", v1)),
	)
	wantErr(t, err, ErrMismatch)
	if got := resolveStr(t, s, v1); got != "Var(1)" {
		t.Fatalf("Var(1) should be unbound after rollback, got %s", got)
	}
}

// Unify 先完整校验两侧类型（先左后右）再开始合一：
// 右侧非法时左侧不得产生任何绑定。
func TestUnifyValidatesBothSidesBeforeUnifying(t *testing.T) {
	s := mustSession(t, 10)
	v1 := mustNewVar(t, s)
	before := dumpSession(s)
	// 右侧含未登记构造子。
	wantErr(t, s.Unify(v1, Con("Nope")), ErrInvalidArgument)
	// 右侧元数不符。
	wantErr(t, s.Unify(v1, Con("List", v1, v1)), ErrInvalidArgument)
	// 右侧含未知变量编号。
	wantErr(t, s.Unify(v1, Con("List", Var(99))), ErrInvalidArgument)
	// 左侧非法同样拒绝。
	wantErr(t, s.Unify(Var(0), v1), ErrInvalidArgument)
	// 空构造子名字非法。
	wantErr(t, s.Unify(v1, Con("")), ErrInvalidArgument)
	if after := dumpSession(s); after != before {
		t.Fatalf("state changed after rejected Unify:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
