package resume

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// buildPlanner 追加 cur 帧常量 size 的帧（now 与帧号同步），返回规划器。
func buildPlanner(t *testing.T, p, k, c int, g int64, n, cur, size int) *Planner {
	t.Helper()
	q := New(p, k, c, g, n)
	for i := 1; i <= cur; i++ {
		if err := q.Append(int64(i), size); err != nil {
			t.Fatalf("Append(%d) 意外失败: %v", i, err)
		}
	}
	return q
}

// joinDiscard 加入玩家 p1 并立即断线，返回用于重连的令牌。
func joinDiscard(q *Planner) int {
	now := q.now + 1
	if err := q.Join(now, "p1"); err != nil {
		panic(err)
	}
	token, err := q.Disconnect(now, "p1")
	if err != nil {
		panic(err)
	}
	return token
}

func TestPlanSpecExamples(t *testing.T) {
	type tc struct {
		name     string
		have     int
		kind     Kind
		snapTick int
		from, to int
		bytes    int64
		err      error
	}

	// 规格主例：P=10,K=16,C=4,G=5000,cur=37, 每帧 3 字节，s=30 快照大小 30。
	mainCases := []tc{
		{"have=33 增量", 33, Delta, 0, 34, 37, 12, nil},
		{"have=26 代价取等走增量", 26, Delta, 0, 27, 37, 33, nil},
		{"have=25 代价超差走快照", 25, Snapshot, 30, 31, 37, 30 + 21, nil},
		{"have=low-1=21 仍走快照", 21, Snapshot, 30, 31, 37, 30 + 21, nil},
		{"have=low-2=20 帧21已丢走快照", 20, Snapshot, 30, 31, 37, 30 + 21, nil},
		{"have=30 不小于s走增量", 30, Delta, 0, 31, 37, 21, nil},
		{"have=37 无需补帧", 37, None, 0, 38, 37, 0, nil},
		{"have=38 超前", 38, None, 0, 0, 0, 0, ErrAhead},
	}
	for _, c := range mainCases {
		t.Run(c.name, func(t *testing.T) {
			q := buildPlanner(t, 10, 16, 4, 5000, 4, 37, 3)
			token := joinDiscard(q)
			got, err := q.Reconnect(100, "p1", token, c.have)
			if err != c.err {
				t.Fatalf("err=%v 期望 %v", err, c.err)
			}
			if c.err == nil && (got.Kind != c.kind || got.SnapTick != c.snapTick ||
				got.From != c.from || got.To != c.to || got.Bytes != c.bytes) {
				t.Fatalf("计划=%+v 期望 kind=%d snap=%d [%d,%d] bytes=%d",
					got, c.kind, c.snapTick, c.from, c.to, c.bytes)
			}
		})
	}

	t.Run("缓冲边界 前一格增量再前一格快照", func(t *testing.T) {
		// C=100 排除代价因素，纯看缓冲：low=22。
		q := buildPlanner(t, 10, 16, 100, 5000, 4, 37, 3)
		token := joinDiscard(q)
		p21, err := q.Reconnect(100, "p1", token, 21)
		if err != nil || p21.Kind != Delta || p21.From != 22 || p21.To != 37 || p21.Bytes != 16*3 {
			t.Fatalf("have=21: %+v err=%v", p21, err)
		}
		q2 := buildPlanner(t, 10, 16, 100, 5000, 4, 37, 3)
		tk2 := joinDiscard(q2)
		p20, err := q2.Reconnect(100, "p1", tk2, 20)
		if err != nil || p20.Kind != Snapshot || p20.SnapTick != 30 || p20.From != 31 || p20.Bytes != 30+21 {
			t.Fatalf("have=20: %+v err=%v", p20, err)
		}
	})

	t.Run("cur=7 尚无快照 全量增量", func(t *testing.T) {
		q := buildPlanner(t, 10, 16, 4, 5000, 4, 7, 3)
		token := joinDiscard(q)
		got, err := q.Reconnect(100, "p1", token, 0)
		if err != nil || got.Kind != Delta || got.From != 1 || got.To != 7 || got.Bytes != 21 {
			t.Fatalf("%+v err=%v", got, err)
		}
	})

	t.Run("cur=40 s=cur 空区间仅快照字节", func(t *testing.T) {
		q := buildPlanner(t, 10, 16, 4, 5000, 4, 40, 3)
		token := joinDiscard(q)
		got, err := q.Reconnect(100, "p1", token, 10)
		if err != nil || got.Kind != Snapshot || got.SnapTick != 40 ||
			got.From != 41 || got.To != 40 || got.Bytes != 30 {
			t.Fatalf("%+v err=%v", got, err)
		}
	})
}

func TestGraceTokensAndAck(t *testing.T) {
	t.Run("宽限取等与令牌序列", func(t *testing.T) {
		q := New(10, 16, 4, 5000, 4)
		if err := q.Join(0, "p1"); err != nil {
			t.Fatal(err)
		}
		token, err := q.Disconnect(1000, "p1")
		if err != nil || token != 0 {
			t.Fatalf("首次断线 token=%d err=%v, 期望 0", token, err)
		}
		plan, err := q.Reconnect(5999, "p1", 0, 0)
		if err != nil || plan.Kind != None { // 尚无帧：have=cur=0，无需补帧
			t.Fatalf("5999 重连失败: %+v %v", plan, err)
		}
		if q.players["p1"].gen != 1 {
			t.Fatalf("重连后 gen=%d 期望 1", q.players["p1"].gen)
		}
		token1, err := q.Disconnect(6000, "p1")
		if err != nil || token1 != 1 {
			t.Fatalf("二次断线 token=%d err=%v", token1, err)
		}
		if _, err := q.Reconnect(6001, "p1", 0, 0); err != ErrBadToken {
			t.Fatalf("旧令牌重连 err=%v 期望 ErrBadToken", err)
		}
		if _, err := q.Reconnect(6002, "p1", 1, 0); err != nil {
			t.Fatalf("正确令牌重连失败: %v", err)
		}
	})

	t.Run("宽限取等离场后重入gen=1", func(t *testing.T) {
		q := New(10, 16, 4, 5000, 4)
		if err := q.Join(0, "p1"); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Disconnect(1000, "p1"); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Reconnect(6000, "p1", 0, 0); err != ErrInvalidState {
			t.Fatalf("取等时刻重连 err=%v 期望 ErrInvalidState（已离场）", err)
		}
		if err := q.Join(6000, "p1"); err != nil {
			t.Fatalf("离场后 Join 失败: %v", err)
		}
		if q.players["p1"].gen != 1 || q.players["p1"].ack != 0 || !q.players["p1"].online {
			t.Fatalf("重入状态错误: %+v", q.players["p1"])
		}
	})

	t.Run("ack回退静默且仅重连可变小", func(t *testing.T) {
		q := buildPlanner(t, 10, 16, 4, 5000, 4, 10, 1)
		if err := q.Join(10, "p1"); err != nil {
			t.Fatal(err)
		}
		if err := q.Ack(11, "p1", 5); err != nil {
			t.Fatal(err)
		}
		if err := q.Ack(12, "p1", 3); err != nil || q.players["p1"].ack != 5 {
			t.Fatalf("Ack 回退 err=%v ack=%d（期望保持5）", err, q.players["p1"].ack)
		}
		if _, err := q.Disconnect(13, "p1"); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Reconnect(14, "p1", 0, 2); err != nil || q.players["p1"].ack != 2 {
			t.Fatalf("重连置小 ack 失败: %v ack=%d", err, q.players["p1"].ack)
		}
	})
}

func TestRejectionOrderAndNoSideEffect(t *testing.T) {
	t.Run("构造参数非法", func(t *testing.T) {
		bad := [][5]int{
			{0, 16, 4, 5000, 4},        // P 越界
			{10, 9, 4, 5000, 4},        // K<P
			{10, 16, -1, 5000, 4},      // C 越界
			{10, 16, 4, 0, 4},          // G 越界
			{10, 16, 4, 5000, 0},       // N 越界
			{10001, 10001, 4, 5000, 4}, // P 超上限
		}
		for _, b := range bad {
			func() {
				defer func() {
					if recover() == nil {
						t.Fatalf("参数 %v 应当 panic(ErrInvalidArg)", b)
					}
				}()
				New(b[0], b[1], b[2], int64(b[3]), b[4])
			}()
		}
	})

	t.Run("参数非法优先于时钟回退", func(t *testing.T) {
		q := buildPlanner(t, 10, 16, 4, 5000, 4, 5, 1)
		if err := q.Append(3, -1); err != ErrInvalidArg {
			t.Fatalf("err=%v 期望 ErrInvalidArg", err)
		}
		if err := q.Ack(3, "", 0); err != ErrInvalidArg {
			t.Fatalf("空玩家 err=%v 期望 ErrInvalidArg", err)
		}
		if err := q.Append(4, 1_000_001); err != ErrInvalidArg {
			t.Fatalf("size 越界 err=%v", err)
		}
	})

	t.Run("时钟回退优先于玩家不存在", func(t *testing.T) {
		q := buildPlanner(t, 10, 16, 4, 5000, 4, 5, 1)
		if err := q.Ack(1, "ghost", 1); err != ErrClockRewind {
			t.Fatalf("err=%v 期望 ErrClockRewind", err)
		}
	})

	t.Run("玩家不存在优先于状态不符", func(t *testing.T) {
		q := buildPlanner(t, 10, 16, 4, 5000, 4, 5, 1)
		if err := q.Ack(6, "ghost", 1); err != ErrNoPlayer {
			t.Fatalf("err=%v 期望 ErrNoPlayer", err)
		}
		if _, err := q.Disconnect(6, "ghost"); err != ErrNoPlayer {
			t.Fatalf("断线不存在玩家 err=%v", err)
		}
	})

	t.Run("状态不符优先于令牌与超前", func(t *testing.T) {
		q := buildPlanner(t, 10, 16, 4, 5000, 4, 5, 1)
		if err := q.Join(6, "p1"); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Reconnect(7, "p1", 99, 99); err != ErrInvalidState {
			t.Fatalf("在线重连 err=%v 期望 ErrInvalidState", err)
		}
		if _, err := q.Disconnect(8, "p1"); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Reconnect(9, "p1", 99, 99); err != ErrBadToken {
			t.Fatalf("错令牌 err=%v 期望 ErrBadToken", err)
		}
	})

	t.Run("Join在场优先于满员", func(t *testing.T) {
		q := New(10, 16, 4, 5000, 1)
		if err := q.Join(0, "p1"); err != nil {
			t.Fatal(err)
		}
		if err := q.Join(1, "p1"); err != ErrInvalidState {
			t.Fatalf("重复 Join err=%v 期望 ErrInvalidState", err)
		}
		if err := q.Join(2, "p2"); err != ErrRoomFull {
			t.Fatalf("满员 Join err=%v 期望 ErrRoomFull", err)
		}
	})

	t.Run("被拒不改状态不推进时钟", func(t *testing.T) {
		q := buildPlanner(t, 10, 16, 4, 5000, 4, 5, 1)
		if err := q.Join(6, "p1"); err != nil {
			t.Fatal(err)
		}
		// 时钟回退被拒：now 仍停在 6，后续 now=6 操作合法
		if err := q.Ack(1, "p1", 3); err != ErrClockRewind {
			t.Fatalf("err=%v 期望 ErrClockRewind", err)
		}
		if err := q.Ack(6, "p1", 3); err != nil {
			t.Fatalf("被拒后时钟未推进，now=6 应仍合法: %v", err)
		}
		if q.players["p1"].ack != 3 {
			t.Fatalf("ack=%d 期望 3", q.players["p1"].ack)
		}
		// 超前 Ack 被拒不改 ack
		if err := q.Ack(7, "p1", 6); err != ErrAhead {
			t.Fatalf("超前 Ack err=%v", err)
		}
		if q.players["p1"].ack != 3 {
			t.Fatalf("超前拒绝后 ack=%d 期望仍为 3", q.players["p1"].ack)
		}
		// 错误参数 Append 被拒不增帧
		if err := q.Append(8, -1); err != ErrInvalidArg {
			t.Fatal(err)
		}
		if q.ring.Cur() != 5 {
			t.Fatalf("被拒 Append 后 cur=%d 期望仍为 5", q.ring.Cur())
		}
	})
}

// naivePlanner 是朴素模拟：保存全部帧并逐帧累加，规则与 Planner 相同。
type naivePlanner struct {
	p, c  int
	k     int
	g     int64
	n     int
	now   int64
	sizes []int // 帧号从 1 开始，sizes[i-1]
	pl    map[string]*naivePlayer
}

type naivePlayer struct {
	online bool
	ack    int
	gen    int
	discAt int64
}

func newNaive(p, k, c int, g int64, n int) *naivePlanner {
	return &naivePlanner{p: p, k: k, c: c, g: g, n: n, pl: map[string]*naivePlayer{}}
}

func (m *naivePlanner) cur() int { return len(m.sizes) }

func (m *naivePlanner) snapTick() int {
	c := m.cur()
	return c / m.p * m.p
}

func (m *naivePlanner) snapSize(s int) int64 {
	var sum int64
	for i := s - m.p + 1; i <= s; i++ {
		sum += int64(m.sizes[i-1])
	}
	return sum
}

func (m *naivePlanner) rangeSum(from, to int) int64 {
	var sum int64
	for i := from; i <= to; i++ {
		sum += int64(m.sizes[i-1])
	}
	return sum
}

func (m *naivePlanner) present(pl *naivePlayer, now int64) bool {
	return pl.online || now < pl.discAt+m.g
}

func (m *naivePlanner) countPresent(now int64) int {
	n := 0
	for _, pl := range m.pl {
		if m.present(pl, now) {
			n++
		}
	}
	return n
}

type naivePlan struct {
	kind     Kind
	snapTick int
	from, to int
	bytes    int64
}

func (m *naivePlanner) append(now int64, size int) error {
	if size < 0 || size > 1_000_000 {
		return ErrInvalidArg
	}
	if now < m.now {
		return ErrClockRewind
	}
	m.sizes = append(m.sizes, size)
	m.now = now
	return nil
}

func (m *naivePlanner) join(now int64, name string) error {
	if name == "" {
		return ErrInvalidArg
	}
	if now < m.now {
		return ErrClockRewind
	}
	if pl, ok := m.pl[name]; ok && m.present(pl, now) {
		return ErrInvalidState
	}
	if m.countPresent(now) >= m.n {
		return ErrRoomFull
	}
	pl := m.pl[name]
	if pl == nil {
		pl = &naivePlayer{}
		m.pl[name] = pl
	} else {
		pl.gen++
	}
	pl.online = true
	pl.ack = 0
	m.now = now
	return nil
}

func (m *naivePlanner) ack(now int64, name string, a int) error {
	if name == "" || a < 0 {
		return ErrInvalidArg
	}
	if now < m.now {
		return ErrClockRewind
	}
	pl, ok := m.pl[name]
	if !ok {
		return ErrNoPlayer
	}
	if !m.present(pl, now) {
		return ErrInvalidState
	}
	if !pl.online {
		return ErrInvalidState
	}
	if a > m.cur() {
		return ErrAhead
	}
	if a > pl.ack {
		pl.ack = a
	}
	m.now = now
	return nil
}

func (m *naivePlanner) disconnect(now int64, name string) (int, error) {
	if name == "" {
		return 0, ErrInvalidArg
	}
	if now < m.now {
		return 0, ErrClockRewind
	}
	pl, ok := m.pl[name]
	if !ok {
		return 0, ErrNoPlayer
	}
	if !m.present(pl, now) {
		return 0, ErrInvalidState
	}
	if !pl.online {
		return 0, ErrInvalidState
	}
	pl.online = false
	pl.discAt = now
	m.now = now
	return pl.gen, nil
}

func (m *naivePlanner) reconnect(now int64, name string, token, have int) (naivePlan, error) {
	if name == "" || have < 0 {
		return naivePlan{}, ErrInvalidArg
	}
	if now < m.now {
		return naivePlan{}, ErrClockRewind
	}
	pl, ok := m.pl[name]
	if !ok {
		return naivePlan{}, ErrNoPlayer
	}
	if !m.present(pl, now) {
		return naivePlan{}, ErrInvalidState
	}
	if pl.online {
		return naivePlan{}, ErrInvalidState
	}
	if token != pl.gen {
		return naivePlan{}, ErrBadToken
	}
	cur := m.cur()
	if have > cur {
		return naivePlan{}, ErrAhead
	}
	plan := m.makePlan(have, cur)
	pl.online = true
	pl.gen++
	pl.ack = have
	m.now = now
	return plan, nil
}

func (m *naivePlanner) makePlan(have, cur int) naivePlan {
	if have == cur {
		return naivePlan{kind: None, from: cur + 1, to: cur}
	}
	n1 := cur - have
	s := m.snapTick()
	low := cur - m.k + 1 // k 由参数保存
	if low < 1 {
		low = 1
	}
	if have+1 >= low && (s == 0 || have >= s || n1 <= (cur-s)+m.c) {
		return naivePlan{kind: Delta, from: have + 1, to: cur, bytes: m.rangeSum(have+1, cur)}
	}
	return naivePlan{kind: Snapshot, snapTick: s, from: s + 1, to: cur,
		bytes: m.snapSize(s) + m.rangeSum(s+1, cur)}
}

// TestRandomDifferential 以 1500 组随机操作序列对照朴素模拟，日志打印输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	const seq = 1500
	rng := rand.New(rand.NewSource(20261005))
	players := []string{"a", "b", "c", "d"}

	for iter := 0; iter < seq; iter++ {
		p := 1 + rng.Intn(12)
		k := p + rng.Intn(20)
		c := rng.Intn(6)
		g := int64(1 + rng.Intn(10))
		n := 1 + rng.Intn(4)
		real := New(p, k, c, g, n)
		naive := newNaive(p, k, c, g, n)

		var log []string
		steps := 20 + rng.Intn(60)
		for step := 0; step < steps; step++ {
			now := int64(0)
			if real.now > 0 || rng.Intn(3) == 0 {
				now = real.now + int64(rng.Intn(4)) - 1
				if now < 0 {
					now = 0
				}
			}
			op := rng.Intn(10)
			name := players[rng.Intn(len(players))]
			switch {
			case op < 4: // Append
				size := rng.Intn(20)
				if rng.Intn(20) == 0 {
					size = -1
				}
				e1 := real.Append(now, size)
				e2 := naive.append(now, size)
				log = append(log, fmtStep(e1, "Append now=%d size=%d", now, size))
				if !sameErr(e1, e2) {
					t.Fatalf("iter=%d step=%d Append 错误不一致 real=%v naive=%v\n%s",
						iter, step, e1, e2, joinLog(log))
				}
			case op < 6: // Join
				badName := name
				if rng.Intn(15) == 0 {
					badName = ""
				}
				e1 := real.Join(now, badName)
				e2 := naive.join(now, badName)
				log = append(log, fmtStep(e1, "Join now=%d player=%q", now, badName))
				if !sameErr(e1, e2) {
					t.Fatalf("iter=%d step=%d Join 错误不一致 real=%v naive=%v\n%s",
						iter, step, e1, e2, joinLog(log))
				}
			case op < 7: // Ack
				a := rng.Intn(naive.cur() + 3)
				e1 := real.Ack(now, name, a)
				e2 := naive.ack(now, name, a)
				log = append(log, fmtStep(e1, "Ack now=%d player=%q a=%d", now, name, a))
				if !sameErr(e1, e2) {
					t.Fatalf("iter=%d step=%d Ack 错误不一致 real=%v naive=%v\n%s",
						iter, step, e1, e2, joinLog(log))
				}
			case op < 8: // Disconnect
				t1, e1 := real.Disconnect(now, name)
				t2, e2 := naive.disconnect(now, name)
				log = append(log, fmtStep(e1, "Disconnect now=%d player=%q token=%d", now, name, t1))
				if !sameErr(e1, e2) || (e1 == nil && t1 != t2) {
					t.Fatalf("iter=%d step=%d Disconnect 不一致 real=(%d,%v) naive=(%d,%v)\n%s",
						iter, step, t1, e1, t2, e2, joinLog(log))
				}
			default: // Reconnect
				have := rng.Intn(naive.cur() + 3)
				token := rng.Intn(3) - 1 // -1..2，大量非法令牌
				p1, e1 := real.Reconnect(now, name, token, have)
				p2, e2 := naive.reconnect(now, name, token, have)
				log = append(log, fmtStep(e1,
					"Reconnect now=%d player=%q token=%d have=%d -> kind=%d snap=%d [%d,%d] bytes=%d",
					now, name, token, have, p1.Kind, p1.SnapTick, p1.From, p1.To, p1.Bytes))
				if !sameErr(e1, e2) {
					t.Fatalf("iter=%d step=%d Reconnect 错误不一致 real=%v naive=%v\n%s",
						iter, step, e1, e2, joinLog(log))
				}
				if e1 == nil && (p1.Kind != p2.kind || p1.SnapTick != p2.snapTick ||
					p1.From != p2.from || p1.To != p2.to || p1.Bytes != p2.bytes) {
					t.Fatalf("iter=%d step=%d 计划不一致 real={%d %d [%d,%d] %d} naive={%d %d [%d,%d] %d}\n%s",
						iter, step,
						p1.Kind, p1.SnapTick, p1.From, p1.To, p1.Bytes,
						p2.kind, p2.snapTick, p2.from, p2.to, p2.bytes, joinLog(log))
				}
			}
			// 每次操作后核对时钟、帧号、快照与玩家状态。
			if real.now != naive.now || real.ring.Cur() != naive.cur() ||
				real.snaps.Tick() != naive.snapTick() {
				t.Fatalf("iter=%d step=%d 全局状态漂移 real(now=%d,cur=%d,s=%d) naive(now=%d,cur=%d,s=%d)\n%s",
					iter, step, real.now, real.ring.Cur(), real.snaps.Tick(),
					naive.now, naive.cur(), naive.snapTick(), joinLog(log))
			}
			if cnt := real.presentCount(real.now); cnt > n {
				t.Fatalf("iter=%d step=%d 在场玩家超员: %d > %d", iter, step, cnt, n)
			}
			t.Logf("iter=%d %s", iter, log[len(log)-1])
		}
	}
}

func sameErr(a, b error) bool { return a == b }

func fmtStep(err error, format string, args ...interface{}) string {
	why := "成功"
	if err != nil {
		why = "拒绝:" + err.Error()
	}
	return fmt.Sprintf(format+" 判定=%s", append(args, why)...)
}

func joinLog(log []string) string {
	out := ""
	for i, line := range log {
		out += fmt.Sprintf("  step%d: %s\n", i, line)
	}
	return out
}

// TestConcurrentSafe 在 -race 下验证并发操作等价于某种串行顺序且计划区间有效。
func TestConcurrentSafe(t *testing.T) {
	q := New(4, 8, 1, 1000, 64)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := string(rune('a' + id))
			for i := 0; i < 100; i++ {
				now := int64(id*1000 + i)
				_ = q.Append(now, 2)
				_ = q.Join(now, name)
				q.mu.Lock()
				cur := q.ring.Cur()
				low := q.ring.Low()
				gen := 0
				if pl := q.players[name]; pl != nil {
					gen = pl.gen
				}
				q.mu.Unlock()
				_ = q.Ack(now+1, name, cur)
				if _, err := q.Disconnect(now+2, name); err == nil {
					plan, err := q.Reconnect(now+3, name, gen, 0)
					if err == nil {
						if plan.From < low && plan.From <= plan.To {
							t.Errorf("计划起点 %d 早于 low=%d", plan.From, low)
						}
					}
				}
			}
		}(w)
	}
	wg.Wait()
}
