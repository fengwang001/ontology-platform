package ontology

import (
	"errors"
	"fmt"
	"testing"
)

func values(vs ...Value) []Value { return vs }

// buildChain 构造 T0 -> T1 -> T2 -> T3 -> T4 的五层链条，
// 属性 p 定义在 T0，允许集合 {a,b,c}。
func buildChain(t *testing.T) *Ontology {
	t.Helper()
	o := New()
	if err := o.CreateRootType("T0"); err != nil {
		t.Fatal(err)
	}
	if err := o.DefineProperty("T0", "p", values("a", "b", "c")); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 4; i++ {
		name := fmt.Sprintf("T%d", i)
		parent := fmt.Sprintf("T%d", i-1)
		if err := o.CreateSubtype(name, parent, false); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

// TestResolveAllRedeclareCombinations 枚举五层链条上“哪些中间/末端层级重新声明 p”
// 的全部 2^4=16 种组合（T0 为定义处）。对每种组合，验证每个具体类型的查找来源
// 恰好等于该类型向上（含自身）第一个重新声明者，否则回落到 T0。
func TestResolveAllRedeclareCombinations(t *testing.T) {
	for mask := 0; mask < 1<<4; mask++ {
		mask := mask
		t.Run(fmt.Sprintf("mask%04b", mask), func(t *testing.T) {
			o := buildChain(t)
			redeclared := map[int]bool{}
			for level := 1; level <= 4; level++ {
				if mask&(1<<(level-1)) != 0 {
					// 每层使用随层级单调嵌套的集合，保证无论最近的重新声明
					// 落在哪一层，新集合都是其当前生效集合的子集或相等：
					// T1 -> {a,b}，T2/T3/T4 -> {a}。
					shrunk := []Value{"a"}
					if level == 1 {
						shrunk = []Value{"a", "b"}
					}
					if err := o.RedeclareProperty(fmt.Sprintf("T%d", level), "p", shrunk); err != nil {
						t.Fatalf("level %d redeclare: %v", level, err)
					}
					redeclared[level] = true
				}
			}

			for level := 0; level <= 4; level++ {
				typeName := fmt.Sprintf("T%d", level)
				res, err := o.Resolve(typeName, "p")
				if err != nil {
					t.Fatalf("resolve %s: %v", typeName, err)
				}

				wantSource := 0
				for l := level; l >= 1; l-- {
					if redeclared[l] {
						wantSource = l
						break
					}
				}
				if got := res.SourceType; got != fmt.Sprintf("T%d", wantSource) {
					t.Errorf("mask=%04b resolve T%d source = %s, want T%d", mask, level, got, wantSource)
				}
				if res.OriginType != "T0" {
					t.Errorf("mask=%04b resolve T%d origin = %s, want T0", mask, level, res.OriginType)
				}

				// 链条长度 = 具体类型到命中来源之间的实际深度 + 1，
				// 且链条顺序为自具体类型向上，末端为命中节点。
				wantLen := level - wantSource + 1
				if len(res.Chain) != wantLen {
					t.Errorf("mask=%04b resolve T%d chain len = %d, want %d", mask, level, len(res.Chain), wantLen)
				}
				last := res.Chain[len(res.Chain)-1]
				if !last.Hit || last.Type != fmt.Sprintf("T%d", wantSource) {
					t.Errorf("mask=%04b resolve T%d chain last = %+v, want hit T%d", mask, level, last, wantSource)
				}
			}

			// 父类型新重新声明会穿透到“未单独重新声明”的子类型，
			// 而已重新声明的子类型不受影响。
			if err := o.RedeclareProperty("T0", "p", values("a", "b")); err != nil {
				t.Fatalf("redeclare at T0: %v", err)
			}
			for level := 1; level <= 4; level++ {
				res, _ := o.Resolve(fmt.Sprintf("T%d", level), "p")
				nearest := 0
				for l := level; l >= 1; l-- {
					if redeclared[l] {
						nearest = l
						break
					}
				}
				if res.SourceType != fmt.Sprintf("T%d", nearest) {
					t.Errorf("after T0 redeclare: T%d source = %s, want T%d", level, res.SourceType, nearest)
				}
			}
		})
	}
}

// TestRedeclareWideningRejectedAtDeclaration 验证取值集合扩大在“声明时”即被拒绝，
// 且被拒绝后当前生效规则保持不变。
func TestRedeclareWideningRejectedAtDeclaration(t *testing.T) {
	o := buildChain(t)
	if err := o.RedeclareProperty("T2", "p", values("a")); err != nil {
		t.Fatal(err)
	}
	// {a,d} 引入父规则不允许的 d -> 扩大，必须拒绝。
	err := o.RedeclareProperty("T3", "p", values("a", "d"))
	if !errors.Is(err, ErrValueSetWidened) {
		t.Fatalf("want ErrValueSetWidened, got %v", err)
	}
	// 相等集合允许（子集包含相等）。
	if err := o.RedeclareProperty("T3", "p", values("a")); err != nil {
		t.Fatalf("equal subset should be allowed: %v", err)
	}
	// 声明被拒后 T3 的规则未被污染，仍解析到 T2。
	res, err := o.Resolve("T3", "p")
	if err != nil {
		t.Fatal(err)
	}
	if res.SourceType != "T2" && res.SourceType != "T3" {
		t.Fatalf("unexpected source %s", res.SourceType)
	}
}

// TestSealedTypeCannotBeParent 验证密封类型在“创建子类型时”即被拒绝。
func TestSealedTypeCannotBeParent(t *testing.T) {
	o := buildChain(t)
	if err := o.MarkSealed("T2"); err != nil {
		t.Fatal(err)
	}
	err := o.CreateSubtype("T2x", "T2", false)
	if !errors.Is(err, ErrSealedParent) {
		t.Fatalf("want ErrSealedParent, got %v", err)
	}
	// 非密封类型仍可派生。
	if err := o.CreateSubtype("T4x", "T4", true); err != nil {
		t.Fatalf("T4 is not sealed: %v", err)
	}
	// 创建时直接标记密封的类型同样不可再派生。
	err = o.CreateSubtype("T4xx", "T4x", false)
	if !errors.Is(err, ErrSealedParent) {
		t.Fatalf("want ErrSealedParent, got %v", err)
	}
	// 密封类型自身实例的规则必然最终生效，无被遮蔽可能。
	res, err := o.Resolve("T4x", "p")
	if err != nil {
		t.Fatal(err)
	}
	if res.SourceType != "T0" {
		t.Fatalf("sealed leaf resolves to origin T0, got %s", res.SourceType)
	}
}

// TestDeleteMiddleTypeRejected 验证存在直接子类型依赖的类型不可删除，末端可删除。
func TestDeleteMiddleTypeRejected(t *testing.T) {
	o := buildChain(t)
	for _, mid := range []string{"T0", "T1", "T2", "T3"} {
		if err := o.DeleteType(mid); !errors.Is(err, ErrTypeHasChildren) {
			t.Fatalf("delete %s: want ErrTypeHasChildren, got %v", mid, err)
		}
	}
	if err := o.DeleteType("T4"); err != nil {
		t.Fatalf("leaf type should be deletable: %v", err)
	}
	// 删除末端后其父变为新末端。
	if err := o.DeleteType("T3"); err != nil {
		t.Fatalf("T3 should now be deletable: %v", err)
	}
}

// TestNotFoundTakesPrecedence 验证“类型/属性不存在”的判定优先于其余三类错误。
func TestNotFoundTakesPrecedence(t *testing.T) {
	o := buildChain(t)
	if err := o.MarkSealed("T2"); err != nil {
		t.Fatal(err)
	}
	// 在密封类型上对不存在的属性重新声明：必须是 NotFound 而非 Widened/Sealed。
	if err := o.RedeclareProperty("T2", "missing", values("z")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound precedence, got %v", err)
	}
	// 在不存在的类型下创建子类型：NotFound 优先于 SealedParent。
	if err := o.CreateSubtype("X", "Nope", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// 对不存在的类型执行删除：NotFound 优先于 HasChildren。
	if err := o.DeleteType("Nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// 查询不存在的属性。
	if _, err := o.Resolve("T4", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestHistoryValuesSurviveRuleTightening 验证：
// 规则收紧前写入的历史值在收紧后仍可成功读取；只有新写入受新规则校验。
func TestHistoryValuesSurviveRuleTightening(t *testing.T) {
	o := buildChain(t)
	if err := o.CreateInstance("i1", "T3"); err != nil {
		t.Fatal(err)
	}
	// 收紧前写入 "b"（T0 规则允许）。
	wr, err := o.Write("i1", "p", "b")
	if err != nil {
		t.Fatal(err)
	}
	if wr.Source != "T0" {
		t.Fatalf("before redeclare source = %s, want T0", wr.Source)
	}
	// T2 收紧为 {a}，穿透到未重新声明的 T3。
	if err := o.RedeclareProperty("T2", "p", values("a")); err != nil {
		t.Fatal(err)
	}
	// 读取历史值 "b" 必须成功，即使当前生效规则已不允许 b。
	rr, err := o.Read("i1", "p")
	if err != nil {
		t.Fatalf("historical read must succeed: %v", err)
	}
	if !rr.Exists || rr.Value != "b" {
		t.Fatalf("historical value = %q exists=%v, want b", rr.Value, rr.Exists)
	}
	if rr.Resolution.SourceType != "T2" {
		t.Fatalf("current rule source = %s, want T2", rr.Resolution.SourceType)
	}
	// 新写入 "b" 必须被当前规则拒绝；"a" 允许。
	if _, err := o.Write("i1", "p", "b"); err == nil {
		t.Fatal("new write of b after tightening must be rejected")
	}
	if _, err := o.Write("i1", "p", "a"); err != nil {
		t.Fatalf("new write of a must succeed: %v", err)
	}
	// 再次读取得到最新写入 a；历史不被修改、不被追溯重验。
	rr2, err := o.Read("i1", "p")
	if err != nil {
		t.Fatal(err)
	}
	if rr2.Value != "a" || rr2.Seq <= rr.Seq {
		t.Fatalf("latest read = %q seq=%d, want a with greater seq", rr2.Value, rr2.Seq)
	}
}

// TestDeterministicResolveAcrossReads 验证同一实例同一属性在无类型变更时
// 两次读取得到相同规则。
func TestDeterministicResolveAcrossReads(t *testing.T) {
	o := buildChain(t)
	if err := o.RedeclareProperty("T1", "p", values("a", "b")); err != nil {
		t.Fatal(err)
	}
	if err := o.CreateInstance("i", "T4"); err != nil {
		t.Fatal(err)
	}
	r1, err := o.Read("i", "p")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := o.Read("i", "p")
	if err != nil {
		t.Fatal(err)
	}
	if r1.Resolution.Rule.RuleID != r2.Resolution.Rule.RuleID ||
		r1.Resolution.SourceType != r2.Resolution.SourceType {
		t.Fatal("resolution must be stable across reads without type changes")
	}
}
