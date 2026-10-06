package initorder

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// compareErr 比较两个实现返回的错误是否等价（类别与关键载荷一致）。
func compareErr(t *testing.T, got, want error) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("error mismatch: got %v, want nil", got)
		}
		return
	}
	if got == nil {
		t.Fatalf("error mismatch: got nil, want %v", want)
	}
	for _, sentinel := range []error{ErrInvalidArgument, ErrDuplicateDeclaration, ErrUndeclaredReference, ErrInitializationCycle} {
		if errors.Is(got, sentinel) != errors.Is(want, sentinel) {
			t.Fatalf("error category mismatch: got %v, want %v", got, want)
		}
	}
	var gotDup, wantDup *DuplicateDeclarationError
	if errors.As(want, &wantDup) {
		if !errors.As(got, &gotDup) || gotDup.Name != wantDup.Name {
			t.Fatalf("duplicate error mismatch: got %v, want %v", got, want)
		}
	}
	var gotU, wantU *UndeclaredReferenceError
	if errors.As(want, &wantU) {
		if !errors.As(got, &gotU) || *gotU != *wantU {
			t.Fatalf("undeclared error mismatch: got %+v, want %+v", gotU, wantU)
		}
	}
	var gotC, wantC *InitializationCycleError
	if errors.As(want, &wantC) {
		if !errors.As(got, &gotC) || !equalStrings(gotC.Vars, wantC.Vars) {
			t.Fatalf("cycle error mismatch: got %v, want %v", gotC.Vars, wantC.Vars)
		}
	}
}

// equalStrings 比较字符串切片，nil 与空切片视为相等。
func equalStrings(a, b []string) bool {
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

// equalSolution 比较两个求解结果（次序、变量、依赖依据）。
func equalSolution(a, b *Solution) bool {
	if len(a.Order) != len(b.Order) {
		return false
	}
	for i := range a.Order {
		ua, ub := a.Order[i], b.Order[i]
		if ua.Unit != ub.Unit || !equalStrings(ua.Vars, ub.Vars) || !equalStrings(ua.Deps, ub.Deps) {
			return false
		}
	}
	return true
}

var (
	randomValidNames = []string{
		"a", "b", "c", "d", "e", "x", "y", "z", "w",
		"f", "g", "h", "p", "q", "fn1", "fn2", "v_1", "_x", "__",
		"runtime", "unsafe", "builtin",
	}
	randomInvalidNames = []string{"", "1a", "9", "a-b", "a b", "汉", "é", "_1_2_3!"}
	randomPredeclared  = []string{"runtime", "unsafe", "builtin"}
)

// pickVarName 生成变量名候选：多数合法，掺入空白与非法标识符。
func pickVarName(rng *rand.Rand) string {
	switch roll := rng.Intn(100); {
	case roll < 70:
		return randomValidNames[rng.Intn(len(randomValidNames))]
	case roll < 85:
		return "_"
	default:
		return randomInvalidNames[rng.Intn(len(randomInvalidNames))]
	}
}

// pickRefName 生成引用候选：多数合法（含预声明），掺入空白与非法标识符。
func pickRefName(rng *rand.Rand) string {
	switch roll := rng.Intn(100); {
	case roll < 78:
		return randomValidNames[rng.Intn(len(randomValidNames))]
	case roll < 88:
		return "_"
	default:
		return randomInvalidNames[rng.Intn(len(randomInvalidNames))]
	}
}

func pickNameList(rng *rand.Rand, maxLen int, pick func(*rand.Rand) string) []string {
	n := rng.Intn(maxLen + 1)
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, pick(rng))
	}
	return out
}

// TestRandomAgainstNaive 用大量随机登记序列（含被拒绝的登记）对照
// 优化实现与朴素逐轮扫描模型，日志打印每次输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			pre := pickNameList(rng, 2, func(r *rand.Rand) string {
				return randomPredeclared[r.Intn(len(randomPredeclared))]
			})
			pre = dedupStrings(pre)
			real, err := NewSession(pre)
			if err != nil {
				t.Fatalf("NewSession(%v) failed: %v", pre, err)
			}
			naive := newNaiveSession(pre)
			t.Logf("predeclared: %v", pre)

			for op := 0; op < 80; op++ {
				switch roll := rng.Intn(100); {
				case roll < 45:
					vars := pickNameList(rng, 3, pickVarName)
					refs := pickNameList(rng, 4, pickRefName)
					gotErr := real.RegisterVars(vars, refs)
					wantErr := naive.registerVars(vars, refs)
					t.Logf("op %d: RegisterVars(vars=%v, refs=%v) -> got=%v want=%v", op, vars, refs, gotErr, wantErr)
					compareErr(t, gotErr, wantErr)
				case roll < 75:
					name := pickVarName(rng)
					refs := pickNameList(rng, 4, pickRefName)
					gotErr := real.RegisterFunc(name, refs)
					wantErr := naive.registerFunc(name, refs)
					t.Logf("op %d: RegisterFunc(name=%q, refs=%v) -> got=%v want=%v", op, name, refs, gotErr, wantErr)
					compareErr(t, gotErr, wantErr)
				default:
					gotSol, gotErr := real.Solve()
					wantSol, wantErr := naive.solve()
					t.Logf("op %d: Solve -> gotErr=%v wantErr=%v", op, gotErr, wantErr)
					compareErr(t, gotErr, wantErr)
					if gotErr == nil && wantErr == nil {
						if !equalSolution(gotSol, wantSol) {
							t.Fatalf("solution mismatch:\n got: %v\nwant: %v", gotSol.Order, wantSol.Order)
						}
						for _, u := range gotSol.Order {
							t.Logf("  unit %d vars=%v deps=%v", u.Unit, u.Vars, u.Deps)
						}
					}
				}
			}
		})
	}
}

func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
