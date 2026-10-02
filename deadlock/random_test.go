package deadlock

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// naiveDeadlock 枚举全部完成顺序判定死锁集合的朴素实现：
// 对阻塞进程的每个排列，按顺序尝试授予（存在某备选不大于当前 Work 即可行，
// 授予后 Work 净增该进程持有量），凡在某一排列中可完成的进程都不算死锁。
// 调用时不持有锁。
func naiveDeadlock(m *Manager) []int {
	work := append([]int64(nil), m.avail...)
	var blocked []int
	for p := 0; p < m.P; p++ {
		if !m.alive[p] {
			continue
		}
		if m.blocked[p] {
			blocked = append(blocked, p)
		} else {
			for r := 0; r < m.R; r++ {
				work[r] += m.alloc[p][r]
			}
		}
	}
	canFinish := make(map[int]bool)
	// 直接按排列模拟：对每个排列，从头扫描，能授予就授予（Work 净增持有量），
	// 该排列中所有被授予的进程标记为可完成。
	perm := make([]int, len(blocked))
	used := make([]bool, len(blocked))
	var gen func(k int)
	gen = func(k int) {
		if k == len(blocked) {
			w := append([]int64(nil), work...)
			for _, p := range perm {
				idx := firstFitting(m.pending[p], w)
				if idx < 0 {
					return // 该排列在此中断，后面的进程不可完成
				}
				canFinish[p] = true
				for r := 0; r < m.R; r++ {
					w[r] += m.alloc[p][r]
				}
			}
			return
		}
		for i := range blocked {
			if used[i] {
				continue
			}
			used[i] = true
			perm[k] = blocked[i]
			gen(k + 1)
			used[i] = false
		}
	}
	gen(0)
	var dead []int
	for _, p := range blocked {
		if !canFinish[p] {
			dead = append(dead, p)
		}
	}
	sort.Ints(dead)
	return dead
}

// checkInvariants 校验规格中的不变量（调用时不持有锁）。
func checkInvariants(t *testing.T, m *Manager, ctx string) {
	t.Helper()
	for r := 0; r < m.R; r++ {
		if m.avail[r] < 0 {
			t.Fatalf("%s: avail[%d]=%d < 0", ctx, r, m.avail[r])
		}
		sum := m.avail[r]
		for p := 0; p < m.P; p++ {
			sum += m.alloc[p][r]
		}
		if sum != m.T[r] {
			t.Fatalf("%s: conservation violated at resource %d: %d != %d", ctx, r, sum, m.T[r])
		}
	}
	b := 0
	for p := 0; p < m.P; p++ {
		if !m.alive[p] {
			continue
		}
		if m.rb[p] >= m.L {
			t.Fatalf("%s: alive process %d has rb=%d >= L=%d", ctx, p, m.rb[p], m.L)
		}
		if !m.blocked[p] {
			continue
		}
		b++
		if idx := firstFitting(m.pending[p], m.avail); idx >= 0 {
			t.Fatalf("%s: blocked process %d has satisfiable alt %d (grant fixpoint violated)",
				ctx, p, idx)
		}
	}
	dead := m.detect()
	if m.checks > int64(b*(b+1)/2) {
		t.Fatalf("%s: checks=%d exceeds b(b+1)/2=%d (b=%d)", ctx, m.checks, b*(b+1)/2, b)
	}
	for _, p := range dead {
		if !m.blocked[p] {
			t.Fatalf("%s: deadlocked process %d is not blocked", ctx, p)
		}
	}
}

// TestRandomAgainstNaive 2000 组随机操作序列，逐操作与枚举全部完成顺序的
// 朴素实现对照死锁集合，并校验全部不变量与 checks 上界。
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1193))
	for seq := 0; seq < 2000; seq++ {
		R := 1 + rng.Intn(3)
		P := 2 + rng.Intn(4)
		L := 1 + rng.Intn(3)
		T := make([]int64, R)
		c := make([]int64, R)
		for r := 0; r < R; r++ {
			T[r] = int64(1 + rng.Intn(4))
			c[r] = int64(1 + rng.Intn(5))
		}
		m, err := New(R, T, c, P, L)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		var history []string
		record := func(format string, args ...any) {
			history = append(history, fmt.Sprintf(format, args...))
		}
		record("New(R=%d,T=%v,c=%v,P=%d,L=%d)", R, T, c, P, L)
		fail := func(format string, args ...any) {
			t.Fatalf("seq %d: %s\ninput history:\n  %s",
				seq, fmt.Sprintf(format, args...), strings.Join(history, "\n  "))
		}
		nOps := 20 + rng.Intn(20)
		for i := 0; i < nOps; i++ {
			p := rng.Intn(P)
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4, 5: // Request
				nAlts := 1 + rng.Intn(3)
				alts := make([][]int64, nAlts)
				for a := range alts {
					alt := make([]int64, R)
					for r := 0; r < R; r++ {
						alt[r] = int64(rng.Intn(3))
					}
					alts[a] = alt
				}
				res, err := m.Request(p, alts)
				record("Request(%d,%v)=%+v err=%v", p, alts, res, err)
			case 6, 7: // Release
				vec := make([]int64, R)
				for r := 0; r < R; r++ {
					vec[r] = int64(rng.Intn(2))
				}
				grants, err := m.Release(p, vec)
				record("Release(%d,%v)=%v err=%v", p, vec, grants, err)
			case 8: // Detect
				dead := m.Detect()
				record("Detect()=%v", dead)
			case 9: // Resolve
				steps := m.Resolve()
				record("Resolve()=%+v", steps)
				if got := m.Detect(); len(got) != 0 {
					fail("Detect after Resolve = %v, want empty", got)
				}
			}
			// 逐操作对照朴素实现并校验不变量。
			m.mu.Lock()
			want := naiveDeadlock(m)
			got := m.detect()
			if !reflect.DeepEqual(got, want) {
				basis := naiveBasis(m)
				m.mu.Unlock()
				fail("deadlock set = %v, naive enumeration = %v (basis: %s)", got, want, basis)
			}
			checkInvariants(t, m, fmt.Sprintf("seq %d op %d", seq, i))
			m.mu.Unlock()
		}
		t.Logf("seq %d done: %s", seq, strings.Join(history, " | "))
	}
}

// naiveBasis 给出朴素判定的依据：每个阻塞进程是否存在可完成它的排列。
func naiveBasis(m *Manager) string {
	var parts []string
	for p := 0; p < m.P; p++ {
		if m.alive[p] && m.blocked[p] {
			parts = append(parts, fmt.Sprintf("p%d alloc=%v pending=%v", p, m.alloc[p], m.pending[p]))
		}
	}
	return fmt.Sprintf("avail=%v; blocked: %s", m.avail, strings.Join(parts, "; "))
}
