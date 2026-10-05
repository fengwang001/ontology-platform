package lockstep

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/turn"
)

// naive 是逐毫秒推进的朴素模拟：到齐判定遍历玩家，时钟一毫秒一毫秒地走，
// 与 Engine 的计数优化与跳跃式入口处理相互独立，用于随机对照。
type naive struct {
	n      int
	T      int64
	A, R   int
	Kd     int
	now0   int64
	maxNow int64
	tick   int64
	cur    int
	dl     int64
	active []bool
	miss   []int
	last   [][]byte
	has    []bool
	subs   map[int]map[int][]byte
	logs   []turn.Log
}

func newNaive(n int, T int64, A, R, Kd int, now0 int64) *naive {
	s := &naive{
		n: n, T: T, A: A, R: R, Kd: Kd, now0: now0,
		cur:    1,
		dl:     now0 + T,
		active: make([]bool, n),
		miss:   make([]int, n),
		last:   make([][]byte, n),
		has:    make([]bool, n),
		subs:   make(map[int]map[int][]byte),
	}
	for i := range s.active {
		s.active[i] = true
	}
	return s
}

// arrived 朴素到齐判定：遍历全部玩家。
func (s *naive) arrived() bool {
	none := true
	for p := 0; p < s.n; p++ {
		if !s.active[p] {
			continue
		}
		none = false
		if _, ok := s.subs[s.cur][p]; !ok {
			return false
		}
	}
	return !none
}

func (s *naive) settleOne(ts int64) {
	lg := turn.Log{Turn: s.cur, Ts: ts, Entries: make([]turn.Entry, s.n)}
	for p := 0; p < s.n; p++ {
		if cmd, ok := s.subs[s.cur][p]; ok {
			s.miss[p] = 0
			s.last[p] = append([]byte(nil), cmd...)
			s.has[p] = true
			lg.Entries[p] = turn.Entry{Input: cmd, Src: turn.Real}
			continue
		}
		s.miss[p]++
		if s.miss[p] <= s.R && s.has[p] {
			lg.Entries[p] = turn.Entry{Input: s.last[p], Src: turn.Repeat}
		} else {
			lg.Entries[p] = turn.Entry{Src: turn.Empty}
		}
		if s.miss[p] >= s.Kd {
			s.active[p] = false
		}
	}
	s.logs = append(s.logs, lg)
	delete(s.subs, s.cur)
	s.cur++
	s.dl = ts + s.T
}

func (s *naive) settle(ts int64) {
	for {
		s.settleOne(ts)
		if !s.arrived() {
			return
		}
	}
}

// advanceTo 逐毫秒推进时钟，每到截止时刻立即结算。
func (s *naive) advanceTo(now int64) {
	for s.tick < now {
		s.tick++
		if s.tick >= s.dl {
			s.settle(s.dl)
		}
	}
}

func (s *naive) Submit(now int64, p, k int, cmd []byte) error {
	if p < 0 || p >= s.n || k < 1 || len(cmd) < 1 || len(cmd) > 64 {
		return ErrParam
	}
	if now < s.now0 || now < s.maxNow {
		return ErrClock
	}
	s.advanceTo(now)
	if k < s.cur {
		return ErrLate
	}
	if k > s.cur+s.A {
		return ErrAhead
	}
	if _, ok := s.subs[k][p]; ok {
		return ErrDuplicate
	}
	s.maxNow = now
	if !s.active[p] {
		s.active[p] = true
	}
	m := s.subs[k]
	if m == nil {
		m = make(map[int][]byte)
		s.subs[k] = m
	}
	m[p] = append([]byte(nil), cmd...)
	if s.arrived() {
		s.settle(now)
	}
	return nil
}

func (s *naive) Advance(now int64) ([]turn.Log, error) {
	if now < s.now0 || now < s.maxNow {
		return nil, ErrClock
	}
	base := len(s.logs)
	s.advanceTo(now)
	s.maxNow = now
	return s.logs[base:], nil
}

// checkInvariants 校验全局不变式：回合连续各一次、ts 非降且相邻差 <= T、
// 每回合 N 条记录、重复来源等于该玩家此前最近一次实交。
func checkInvariants(t *testing.T, logs []turn.Log, n int, T int64) {
	t.Helper()
	last := make([][]byte, n)
	has := make([]bool, n)
	var prevTs int64
	for i, lg := range logs {
		if lg.Turn != i+1 {
			t.Fatalf("回合不连续: 第 %d 条 turn=%d", i, lg.Turn)
		}
		if i > 0 {
			if lg.Ts < prevTs {
				t.Fatalf("回合 %d 结算时刻回退: %d < %d", lg.Turn, lg.Ts, prevTs)
			}
			if lg.Ts-prevTs > T {
				t.Fatalf("回合 %d 与上一回合结算间隔 %d 超过 T=%d", lg.Turn, lg.Ts-prevTs, T)
			}
		}
		prevTs = lg.Ts
		if len(lg.Entries) != n {
			t.Fatalf("回合 %d 记录数=%d, 期望=%d", lg.Turn, len(lg.Entries), n)
		}
		for p, en := range lg.Entries {
			switch en.Src {
			case turn.Real:
				last[p] = en.Input
				has[p] = true
			case turn.Repeat:
				if !has[p] || string(en.Input) != string(last[p]) {
					t.Fatalf("回合 %d 玩家 %d: 重复填充不等于最近实交", lg.Turn, p)
				}
			case turn.Empty:
				if len(en.Input) != 0 {
					t.Fatalf("回合 %d 玩家 %d: 空填却有输入", lg.Turn, p)
				}
			}
		}
	}
}

func logsEqual(a, b []turn.Log) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Turn != b[i].Turn || a[i].Ts != b[i].Ts || len(a[i].Entries) != len(b[i].Entries) {
			return false
		}
		for p := range a[i].Entries {
			ea, eb := a[i].Entries[p], b[i].Entries[p]
			if ea.Src != eb.Src || string(ea.Input) != string(eb.Input) {
				return false
			}
		}
	}
	return true
}

// TestRandomVsNaive 小参数下 1000 组随机操作序列，与逐毫秒朴素模拟对照，
// 要求每个操作的判定与全部结算记录逐字节一致。
func TestRandomVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for g := 0; g < 1000; g++ {
		n := 1 + rng.Intn(4)
		T := int64(1 + rng.Intn(30))
		A := rng.Intn(4)
		R := rng.Intn(4)
		kd := 1 + rng.Intn(3)
		now0 := int64(rng.Intn(6))
		eng, err := New(n, T, A, R, kd, now0)
		if err != nil {
			t.Fatalf("组 %d: New 失败: %v", g, err)
		}
		sim := newNaive(n, T, A, R, kd, now0)
		if g < 3 {
			t.Logf("组 %d 参数: N=%d T=%d A=%d R=%d Kd=%d now0=%d", g, n, T, A, R, kd, now0)
		}
		now := now0
		ops := 60 + rng.Intn(120)
		for i := 0; i < ops; i++ {
			now += int64(rng.Intn(9)) // now 单调不降；回退拒绝由场景测试覆盖
			var eErr, sErr error
			if rng.Intn(10) < 7 {
				p := rng.Intn(n+2) - 1
				k := sim.cur + rng.Intn(A+5) - 2
				cmd := make([]byte, rng.Intn(71))
				for j := range cmd {
					cmd[j] = byte('a' + rng.Intn(26))
				}
				eErr = eng.Submit(now, p, k, cmd)
				sErr = sim.Submit(now, p, k, cmd)
				if g < 3 {
					t.Logf("组 %d op %d: Submit(now=%d, p=%d, k=%d, len=%d) -> eng=%v sim=%v",
						g, i, now, p, k, len(cmd), eErr, sErr)
				}
			} else {
				var eLogs, sLogs []turn.Log
				eLogs, eErr = eng.Advance(now)
				sLogs, sErr = sim.Advance(now)
				if !logsEqual(eLogs, sLogs) {
					t.Fatalf("组 %d op %d: Advance(%d) 结算不一致\neng=%v\nsim=%v", g, i, now, eLogs, sLogs)
				}
				if g < 3 {
					t.Logf("组 %d op %d: Advance(%d) -> eng=%v sim=%v 新结算 %d 回合",
						g, i, now, eErr, sErr, len(eLogs))
				}
			}
			if eErr != sErr {
				t.Fatalf("组 %d op %d: 判定不一致 eng=%v sim=%v (now=%d)", g, i, eErr, sErr, now)
			}
		}
		if !logsEqual(eng.Log(1), sim.logs) {
			t.Fatalf("组 %d: 最终日志不一致\neng=%v\nsim=%v", g, eng.Log(1), sim.logs)
		}
		checkInvariants(t, eng.Log(1), n, T)
	}
}

// TestTouchedIndependentOfN 一次未触发结算的 Submit（含恢复活跃）触碰的
// 玩家槽位数不超过 A+2，且不随 N 增长。
func TestTouchedIndependentOfN(t *testing.T) {
	const A = 64
	got := make(map[int]int)
	for _, n := range []int{4, 4096} {
		e, err := New(n, 1000, A, 0, 1, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Submit(0, 1, 1, []byte("p1")); err != nil { // p1 提交回合 1，保持活跃
			t.Fatal(err)
		}
		for k := 3; k <= A+1; k++ { // p0 预提交回合 3..A+1（避开 cur=2）
			before := e.touched
			if err := e.Submit(0, 0, k, []byte("x")); err != nil {
				t.Fatal(err)
			}
			if d := e.touched - before; d > 2 {
				t.Fatalf("N=%d: 活跃玩家普通提交触碰 %d 槽位, 期望 <=2", n, d)
			}
		}
		if _, err := e.Advance(1000); err != nil { // 回合 1 超时，p0 缺失 m=1>=Kd 掉线
			t.Fatal(err)
		}
		before := e.touched
		if err := e.Submit(1000, 0, 2, []byte("y")); err != nil { // 恢复活跃 + 提交
			t.Fatal(err)
		}
		got[n] = e.touched - before
		if got[n] > A+2 {
			t.Fatalf("N=%d: 恢复活跃并提交触碰 %d 槽位, 超过 A+2=%d", n, got[n], A+2)
		}
		if len(e.Log(2)) != 0 {
			t.Fatalf("N=%d: 该 Submit 不应触发结算", n)
		}
	}
	if got[4] != got[4096] {
		t.Fatalf("触碰数随 N 增长: N=4 时 %d, N=4096 时 %d", got[4], got[4096])
	}
	t.Logf("touched: N=4 与 N=4096 均为 %d (<= A+2=%d)", got[4], A+2)
}

// TestConcurrent 并发烟雾测试：结果须等价于某串行顺序（不变式仍成立）。
func TestConcurrent(t *testing.T) {
	e, err := New(8, 5, 4, 2, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	var now atomic.Int64
	var wg sync.WaitGroup
	for id := 0; id < 8; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id) + 1))
			for i := 0; i < 200; i++ {
				nw := now.Add(int64(r.Intn(3)))
				if r.Intn(2) == 0 {
					_ = e.Submit(nw, r.Intn(8), 1+r.Intn(10), []byte("x"))
				} else {
					_, _ = e.Advance(nw)
				}
			}
		}(id)
	}
	wg.Wait()
	checkInvariants(t, e.Log(1), 8, 5)
}
