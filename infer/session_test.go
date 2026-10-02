package infer

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func testCtors() map[string]int {
	return map[string]int{"Int": 0, "Bool": 0, "Void": 0, "List": 1, "Fn": 2, "Pair": 2}
}

func mustSession(t *testing.T, maxVar int) *Session {
	t.Helper()
	s, err := NewSession(testCtors(), maxVar)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return s
}

func mustNewVar(t *testing.T, s *Session) Type {
	t.Helper()
	v, err := s.NewVar()
	if err != nil {
		t.Fatalf("NewVar: %v", err)
	}
	return v
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, err, kind error) {
	t.Helper()
	if !errors.Is(err, kind) {
		t.Fatalf("want error kind %v, got %v", kind, err)
	}
}

func resolveStr(t *testing.T, s *Session, ty Type) string {
	t.Helper()
	r, err := s.Resolve(ty)
	if err != nil {
		t.Fatalf("Resolve(%s): %v", ty, err)
	}
	return r.String()
}

func mustLevel(t *testing.T, s *Session, id int) int {
	t.Helper()
	l, err := s.Level(id)
	if err != nil {
		t.Fatalf("Level(%d): %v", id, err)
	}
	return l
}

func mustLookupStr(t *testing.T, s *Session, name string) string {
	t.Helper()
	ty, err := s.Lookup(name)
	if err != nil {
		t.Fatalf("Lookup(%q): %v", name, err)
	}
	return resolveStr(t, s, ty)
}

// 构造参数校验：构造子表与变量上限的边界。
func TestNewSessionValidation(t *testing.T) {
	longName := string(make([]byte, 33))
	cases := []struct {
		ctors  map[string]int
		maxVar int
	}{
		{map[string]int{}, 10},            // 至少 1 个构造子
		{testCtors(), 0},                  // V 至少 1
		{testCtors(), 1_000_001},          // V 至多 10^6
		{map[string]int{"": 0}, 10},       // 空构造子名
		{map[string]int{longName: 0}, 10}, // 名字超 32 字节
		{map[string]int{"A": 5}, 10},      // 元数超 4
		{map[string]int{"A": -1}, 10},     // 元数为负
	}
	for i, c := range cases {
		if _, err := NewSession(c.ctors, c.maxVar); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("case %d: want ErrInvalidArgument, got %v", i, err)
		}
	}
	many := map[string]int{}
	for i := 0; i < 17; i++ {
		many[fmt.Sprintf("C%d", i)] = 0
	}
	if _, err := NewSession(many, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("17 constructors: want ErrInvalidArgument, got %v", err)
	}
	sixteen := map[string]int{}
	for i := 0; i < 16; i++ {
		sixteen[fmt.Sprintf("C%d", i)] = 0
	}
	if _, err := NewSession(sixteen, 1); err != nil {
		t.Fatalf("16 constructors should be allowed: %v", err)
	}
}

// 并发 NewVar：编号严格递增、无空洞、恰好达到上限。
func TestConcurrentNewVar(t *testing.T) {
	const limit = 5000
	s := mustSession(t, limit)
	ids := make(chan int, limit)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				v, err := s.NewVar()
				if err != nil {
					wantErr(t, err, ErrVarLimit)
					return
				}
				ids <- v.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	seen := make(map[int]bool)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate variable id %d", id)
		}
		seen[id] = true
	}
	if len(seen) != limit {
		t.Fatalf("got %d variables, want %d", len(seen), limit)
	}
	for i := 1; i <= limit; i++ {
		if !seen[i] {
			t.Fatalf("missing variable id %d", i)
		}
	}
}

// 并发混合操作：在竞态检测器下运行，验证可串行化且无数据竞争。
func TestConcurrentMixedOps(t *testing.T) {
	s := mustSession(t, 20000)
	must(t, s.Enter())
	shared := mustNewVar(t, s)
	must(t, s.Leave())
	must(t, s.Bind("id", Con("Fn", shared, shared), false))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				v, err := s.NewVar()
				if err != nil {
					return
				}
				_ = s.Unify(v, Con("List", shared))
				ty, err := s.Lookup("id")
				if err == nil {
					_, _ = s.Resolve(ty)
				}
				_, _ = s.Level(1)
				_ = g
			}
		}(g)
	}
	wg.Wait()
	// 未绑定变量的层级只会变小：并发合一可能把它从 1 降到 0。
	if l := mustLevel(t, s, 1); l < 0 || l > 1 {
		t.Fatalf("shared var level = %d, want 0 or 1", l)
	}
}
