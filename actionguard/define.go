package actionguard

import (
	"fmt"
	"strings"
)

// DefinitionError 表示动作声明自相矛盾，在定义（注册）阶段即被拒绝。
type DefinitionError struct {
	ActionType string
	msg        string
}

func (e *DefinitionError) Error() string {
	return fmt.Sprintf("action %q definition error: %s", e.ActionType, e.msg)
}

// evaluateClause 在给定快照上求值一个命名条件，返回是否通过与逐字面量依据。
func evaluateClause(c ConditionClause, snap Snapshot, env map[string]string) (bool, []Evidence) {
	allPass := true
	ev := make([]Evidence, 0, len(c.Lits))
	for _, lit := range c.Lits {
		g := formatAtom(lit.Spec, env)
		got := snap.Atom(g)
		ev = append(ev, Evidence{GroundedAtom: g, Observed: got, Expect: lit.Expect})
		if got != lit.Expect {
			allPass = false
		}
	}
	return allPass, ev
}

// checkConsistency 在动作定义期检查同一阶段内是否存在结构性矛盾。
// 所有声明的条件必须共同成立动作才可执行，因此任一字面量与其他
// 字面量（无论是否在同一个命名条件内）模板可合一且极性相反，
// 都意味着不存在任何“自洽的通过集合”，必须在定义期报错。
//
// 复杂度：字面量数为 n、模板实参数为 k 时为 O(n^2 * k)，
// 只依赖动作声明本身，不读取 Store，也不遍历任何历史调用。
func checkConsistency(a *Action) error {
	// 注册期没有具体输入：用 nil 调用工厂，工厂返回的字面量仍携带
	// VarArg 模板，合一在模板层面进行，因此 nil 输入不影响检查能力。
	preClauses, err := a.Pre(nil)
	if err != nil {
		return &DefinitionError{ActionType: a.Type, msg: "precondition factory error: " + err.Error()}
	}
	postClauses, err := a.Post(nil)
	if err != nil {
		return &DefinitionError{ActionType: a.Type, msg: "postcondition factory error: " + err.Error()}
	}
	if err := checkPhase(a.Type, "precondition", flatten(preClauses)); err != nil {
		return err
	}
	if err := checkPhase(a.Type, "postcondition", flatten(postClauses)); err != nil {
		return err
	}
	return nil
}

func flatten(clauses []ConditionClause) []Literal {
	var out []Literal
	for _, c := range clauses {
		out = append(out, c.Lits...)
	}
	return out
}

func checkPhase(actionType, phase string, lits []Literal) error {
	for i := 0; i < len(lits); i++ {
		for j := i + 1; j < len(lits); j++ {
			x, y := lits[i], lits[j]
			if x.Expect == y.Expect {
				continue
			}
			if unifiable(x.Spec, y.Spec) {
				return &DefinitionError{
					ActionType: actionType,
					msg: fmt.Sprintf("%s set is self-contradictory: %s conflicts with %s",
						phase, describeSpec(x.Spec), describeSpec(y.Spec)),
				}
			}
		}
	}
	return nil
}

// unifiable 判定两个原子模板是否能在某组变量绑定下成为同一个原子。
// 同一位置上：常量-常量必须相等；变量可以绑定到常量或其他变量。
func unifiable(a, b AtomSpec) bool {
	if a.Kind != b.Kind || len(a.Args) != len(b.Args) {
		return false
	}
	subst := map[string]string{} // VarArg 名 -> 已绑定的目标（"c:"常量 / "v:"变量）
	for i := range a.Args {
		switch x := a.Args[i].(type) {
		case ConstArg:
			switch y := b.Args[i].(type) {
			case ConstArg:
				if x != y {
					return false
				}
			case VarArg:
				if !bind(subst, string(y), "c:"+string(x)) {
					return false
				}
			}
		case VarArg:
			switch y := b.Args[i].(type) {
			case ConstArg:
				if !bind(subst, string(x), "c:"+string(y)) {
					return false
				}
			case VarArg:
				if !bind(subst, string(x), "v:"+string(y)) {
					return false
				}
			}
		}
	}
	return true
}

// bind 在并查集式的替换表上施加 var -> target 的绑定并检查一致性。
func bind(subst map[string]string, v, target string) bool {
	if cur, ok := subst[v]; ok && cur != target {
		return false
	}
	subst[v] = target
	return true
}

func describeSpec(s AtomSpec) string {
	parts := make([]string, 0, len(s.Args)+1)
	parts = append(parts, s.Kind)
	for _, a := range s.Args {
		switch v := a.(type) {
		case ConstArg:
			parts = append(parts, string(v))
		case VarArg:
			parts = append(parts, "?"+string(v))
		}
	}
	return strings.Join(parts, "/")
}
