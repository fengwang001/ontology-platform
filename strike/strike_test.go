package strike

import (
	"sync"
	"testing"
)

func mustStore(t *testing.T, period int64) *Store {
	t.Helper()
	s, err := NewStore(period)
	if err != nil {
		t.Fatalf("NewStore(%d): %v", period, err)
	}
	return s
}

func TestNewStoreParamRange(t *testing.T) {
	for _, p := range []int64{-1, 0, 1e9 + 1, 1e12} {
		if _, err := NewStore(p); err != ErrInvalidArgument {
			t.Fatalf("NewStore(%d) err=%v, want ErrInvalidArgument", p, err)
		}
	}
	for _, p := range []int64{1, 2, 1e9} {
		if _, err := NewStore(p); err != nil {
			t.Fatalf("NewStore(%d): %v", p, err)
		}
	}
}

// 计分在 [t, t+P) 内有效，恰到 t+P 即失效；状态阈值 3/5，不粘滞。
func TestStateWindowAndThresholds(t *testing.T) {
	cases := []struct {
		name    string
		period  int64
		weights []int
		times   []int64
		now     int64
		want    State
	}{
		{"无记录正常", 100, nil, nil, 50, StateNormal},
		{"s=2正常", 100, []int{2}, []int64{10}, 50, StateNormal},
		{"s=3禁言", 100, []int{2, 1}, []int64{10, 20}, 50, StateMuted},
		{"s=4禁言", 100, []int{2, 2}, []int64{10, 20}, 50, StateMuted},
		{"s=5封禁", 100, []int{2, 2, 1}, []int64{10, 20, 30}, 50, StateBanned},
		{"生效起点恰等t", 100, []int{2, 2}, []int64{50, 50}, 50, StateMuted},
		{"恰到t+P失效", 100, []int{2, 2}, []int64{50, 50}, 150, StateNormal},
		{"t+P-1仍有效", 100, []int{2, 2}, []int64{50, 50}, 149, StateMuted},
		{"部分过期不粘滞", 100, []int{2, 2, 2}, []int64{10, 20, 145}, 150, StateNormal},
		{"未来记录不计", 100, []int{2, 2}, []int64{60, 70}, 50, StateNormal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := mustStore(t, tc.period)
			for i, w := range tc.weights {
				s.AddScore(string(rune('a'+i)), "u", w, tc.times[i])
			}
			if got := s.State([]byte("u"), tc.now); got != tc.want {
				t.Fatalf("State=%v, want %v", got, tc.want)
			}
		})
	}
}

// 被推翻的计分自始不计，推翻后任意时刻状态按新的 s 重算。
func TestOverturnRetroactive(t *testing.T) {
	s := mustStore(t, 1000)
	s.AddScore("d1", "u", 2, 10)
	s.AddScore("d2", "u", 2, 20)
	if got := s.State([]byte("u"), 30); got != StateMuted {
		t.Fatalf("before overturn State=%v, want muted", got)
	}
	s.Overturn("d1")
	// 自始不计：查询推翻前的时刻也不含 d1。
	for _, now := range []int64{15, 30, 500} {
		if got := s.State([]byte("u"), now); got != StateNormal {
			t.Fatalf("after overturn State(%d)=%v, want normal", now, got)
		}
	}
	// 推翻不存在的决定是空操作。
	s.Overturn("nope")
	if got := s.State([]byte("u"), 30); got != StateNormal {
		t.Fatalf("State=%v, want normal", got)
	}
}

// 时钟：拒绝回退，接受后推进。
func TestClock(t *testing.T) {
	s := mustStore(t, 10)
	if err := s.CheckClock(5); err != nil {
		t.Fatalf("CheckClock(5): %v", err)
	}
	s.AcceptClock(5)
	if err := s.CheckClock(5); err != nil {
		t.Fatalf("CheckClock(5) equal: %v", err)
	}
	if err := s.CheckClock(4); err != ErrClockRegression {
		t.Fatalf("CheckClock(4) err=%v, want ErrClockRegression", err)
	}
}

// touched 复杂度：一次 State 触碰的记录数不超过有效期内条数+2，
// 与已过期历史条数（100 vs 10000）及其他创作者的记录数无关。
func TestTouchedIndependentOfExpiredHistory(t *testing.T) {
	const period = 1000
	s := mustStore(t, period)
	fill := func(creator string, expired, active int) {
		s.Lock()
		for i := 0; i < expired; i++ {
			s.AddScore(creator+"-old-"+string(rune(i)), creator, 1, int64(i%100)) // t<=99，早已过期
		}
		for i := 0; i < active; i++ {
			s.AddScore(creator+"-new-"+string(rune(i)), creator, 1, 5000+int64(i))
		}
		s.Unlock()
	}
	fill("a", 100, 3)
	fill("b", 10000, 3)
	fill("other", 50000, 0) // 其他创作者的大量记录不得影响 a/b

	touch := func(creator string) (int, State) {
		s.Lock()
		s.touched = 0
		st := s.StateLocked(creator, 5500)
		n := s.touched
		s.Unlock()
		return n, st
	}
	na, sa := touch("a")
	nb, sb := touch("b")
	if sa != StateMuted || sb != StateMuted {
		t.Fatalf("states: a=%v b=%v, want muted", sa, sb)
	}
	if na > 3+2 {
		t.Fatalf("touched(a)=%d > active+2=5", na)
	}
	if na != nb {
		t.Fatalf("touched mismatch: expired=100 -> %d, expired=10000 -> %d", na, nb)
	}
	t.Logf("touched: expired=100 -> %d, expired=10000 -> %d (active=3)", na, nb)
}

// 并发调用等价于某个串行顺序（配合 -race 验证）。
func TestConcurrentStateAndAdd(t *testing.T) {
	s := mustStore(t, 1e9)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = s.State([]byte("u"), int64(i))
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			s.Lock()
			s.AddScore("d"+string(rune(i)), "u", 1, int64(i))
			s.Unlock()
		}
	}()
	wg.Wait()
	if got := s.State([]byte("u"), 199); got != StateBanned {
		t.Fatalf("final State=%v, want banned", got)
	}
}
