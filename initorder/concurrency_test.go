package initorder

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// checkSolutionConsistent 校验求解结果自洽：单元不重复，且每个单元的
// 依赖变量都由次序更靠前的单元完成（预声明标识符不出现在依赖中）。
// 这能发现“看到登记到一半的单元”之类的不一致快照。
func checkSolutionConsistent(t *testing.T, sol *Solution) {
	t.Helper()
	done := map[string]bool{}
	seen := map[int]bool{}
	for _, u := range sol.Order {
		if seen[u.Unit] {
			t.Fatalf("unit %d appears twice in order", u.Unit)
		}
		seen[u.Unit] = true
		for _, d := range u.Deps {
			if !done[d] {
				t.Fatalf("unit %d depends on %q which is not initialized earlier", u.Unit, d)
			}
		}
		for _, v := range u.Vars {
			if !isBlankIdent(v) {
				done[v] = true
			}
		}
	}
}

// TestConcurrentRegisterAndSolve 多 goroutine 并发登记与求解交错执行：
// 每次求解都必须看到一致快照；全部登记完成后，并发会话的最终求解结果
// 必须与按已接受登记次序串行重放的朴素模型完全一致。
func TestConcurrentRegisterAndSolve(t *testing.T) {
	s := mustSession(t, "runtime")

	const goroutines = 8
	const unitsPerG = 30

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 求解方：持续求解直到登记结束，每次结果都必须自洽。
	var solveWg sync.WaitGroup
	for i := 0; i < 4; i++ {
		solveWg.Add(1)
		go func() {
			defer solveWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				sol, err := s.Solve()
				if err != nil {
					t.Errorf("unexpected solve error: %v", err)
					return
				}
				checkSolutionConsistent(t, sol)
			}
		}()
	}

	// 登记方：每个 goroutine 使用独立名字前缀，引用只指向本 goroutine
	// 已接受的名字或预声明标识符，因此任何快照下求解都应成功。
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			prefix := fmt.Sprintf("g%d_", g)
			var avail []string // 本 goroutine 已接受的名字
			for i := 0; i < unitsPerG; i++ {
				name := fmt.Sprintf("%sv%d", prefix, i)
				refs := append([]string{"runtime"}, avail...)
				if err := s.RegisterVars([]string{name}, refs); err != nil {
					t.Errorf("RegisterVars(%q) failed: %v", name, err)
					return
				}
				avail = append(avail, name)
				if i%3 == 0 && len(avail) > 1 {
					fname := fmt.Sprintf("%sf%d", prefix, i)
					if err := s.RegisterFunc(fname, avail[:len(avail)-1]); err != nil {
						t.Errorf("RegisterFunc(%q) failed: %v", fname, err)
						return
					}
					avail = append(avail, fname)
				}
			}
		}(g)
	}
	wg.Wait()
	close(stop)
	solveWg.Wait()

	// 最终状态：真实求解结果必须与按已接受登记次序串行重放的朴素模型一致。
	sol, err := s.Solve()
	if err != nil {
		t.Fatalf("final Solve failed: %v", err)
	}
	checkSolutionConsistent(t, sol)
	if len(sol.Order) != goroutines*unitsPerG {
		t.Fatalf("final order has %d units, want %d", len(sol.Order), goroutines*unitsPerG)
	}

	naive := newNaiveSession([]string{"runtime"})
	s.mu.RLock()
	for _, d := range s.decls {
		if d.kind == DeclKindVars {
			u := s.units[d.index]
			if err := naive.registerVars(u.vars, u.refs); err != nil {
				s.mu.RUnlock()
				t.Fatalf("naive replay RegisterVars failed: %v", err)
			}
		} else {
			f := s.funcs[d.index]
			if err := naive.registerFunc(f.name, f.refs); err != nil {
				s.mu.RUnlock()
				t.Fatalf("naive replay RegisterFunc failed: %v", err)
			}
		}
	}
	s.mu.RUnlock()
	wantSol, err := naive.solve()
	if err != nil {
		t.Fatalf("naive solve failed: %v", err)
	}
	if !equalSolution(sol, wantSol) {
		t.Fatalf("concurrent result differs from serial replay")
	}
}

// TestConcurrentDuplicateRegistration 并发登记同名声明：恰好一个成功，
// 其余全部收到重复声明错误，且最终状态与任一串行化等价。
func TestConcurrentDuplicateRegistration(t *testing.T) {
	s := mustSession(t)
	const contenders = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.RegisterVars([]string{"shared"}, nil)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes++
			} else if !isDupErr(err) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successes = %d, want exactly 1", successes)
	}
	if got := solveUnits(t, s); len(got) != 1 {
		t.Fatalf("order = %v, want exactly one unit", got)
	}
}

func isDupErr(err error) bool {
	return err != nil && errors.Is(err, ErrDuplicateDeclaration)
}
