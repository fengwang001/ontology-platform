package registry

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/policy"
)

// simRec/sim 是严格按题目步骤逐条实现的独立朴素参考模型。
type simRec struct {
	run   uint64
	owner string
	state policy.State
	endAt int64
}

type sim struct {
	r, n, clock int64
	nextRun     uint64
	live        map[string]*simRec
	admins      map[string]bool
}

func newSim(R int64, N int) *sim {
	return &sim{r: R, n: int64(N), live: map[string]*simRec{}, admins: map[string]bool{}}
}

func (m *sim) purge(now int64) {
	for id, e := range m.live {
		if e.state != policy.Running && now >= e.endAt+m.r {
			delete(m.live, id)
		}
	}
}

func (m *sim) start(id, p string, ru policy.ReusePolicy, co policy.ConflictPolicy, now int64) (uint64, error) {
	if now < m.clock {
		return 0, ErrClock
	}
	m.purge(now)
	e, exists := m.live[id]
	if exists && e.state == policy.Running {
		switch co {
		case policy.Fail:
			return 0, ErrRunning
		case policy.UseExisting:
			m.clock = now
			return e.run, nil
		case policy.Terminate:
			if p != e.owner && !m.admins[p] {
				return 0, ErrDenied
			}
			e.state, e.endAt = policy.Terminated, now
		}
	} else if exists {
		if !(ru == policy.AllowAll || (ru == policy.AllowFailedOnly && e.state != policy.Completed)) {
			return 0, ErrReuse
		}
	} else {
		if int64(len(m.live)) >= m.n {
			return 0, ErrCapacity
		}
		m.nextRun++
		m.live[id] = &simRec{run: m.nextRun, owner: p, state: policy.Running}
		m.clock = now
		return m.nextRun, nil
	}
	m.nextRun++ // Running 已终止替换 或 已结束复用替换；不检查容量
	m.live[id] = &simRec{run: m.nextRun, owner: p, state: policy.Running}
	m.clock = now
	return m.nextRun, nil
}

func (m *sim) finish(id string, run uint64, st policy.State, now int64) error {
	if !policy.ValidFinishState(st) {
		return ErrArgument
	}
	if now < m.clock {
		return ErrClock
	}
	m.purge(now)
	e, ok := m.live[id]
	if !ok {
		return ErrNotFound
	}
	if e.run != run {
		return ErrStale
	}
	if e.state != policy.Running {
		return ErrNotRunning
	}
	e.state, e.endAt = st, now
	m.clock = now
	return nil
}

// TestRandomAgainstNaiveSim 随机操作序列与朴素逐步模拟逐操作对照（结果与状态）。
func TestRandomAgainstNaiveSim(t *testing.T) {
	principals, ids := []string{"p1", "p2", "p3", "boss"}, []string{"a", "b", "c"}
	for seed := int64(0); seed < 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		R, N := int64(1+rng.Intn(20)), 1+rng.Intn(4)
		g, m := newTest(t, R, N), newSim(R, N)
		var now int64
		for step := 0; step < 300; step++ {
			if rng.Intn(8) == 0 {
				now--
			} else {
				now += int64(rng.Intn(3))
			}
			if now < 0 {
				now = 0
			}
			id, p := ids[rng.Intn(3)], principals[rng.Intn(4)]
			var r1, r2 uint64
			var e1, e2 error
			switch rng.Intn(6) {
			case 0, 1, 2:
				ru, co := policy.ReusePolicy(rng.Intn(3)), policy.ConflictPolicy(rng.Intn(3))
				r1, e1 = g.Start([]byte(id), []byte(p), ru, co, now)
				r2, e2 = m.start(id, p, ru, co, now)
			case 3, 4:
				st := []policy.State{policy.Completed, policy.Failed, policy.Cancelled,
					policy.Running, policy.Terminated}[rng.Intn(5)]
				run := uint64(1 + rng.Intn(int(m.nextRun)+2))
				if rng.Intn(2) == 0 {
					if e, ok := m.live[id]; ok {
						run = e.run
					}
				}
				e1, e2 = g.Finish([]byte(id), run, st, now), m.finish(id, run, st, now)
			case 5:
				if rng.Intn(2) == 0 {
					m.admins[p] = true
					_ = g.acl.Grant([]byte(p))
				} else {
					delete(m.admins, p)
					_ = g.acl.Revoke([]byte(p))
				}
			}
			t.Logf("seed=%d step=%d now=%d id=%s p=%s -> real=(%d,%v) sim=(%d,%v)",
				seed, step, now, id, p, r1, e1, r2, e2)
			if r1 != r2 || errorMsg(e1) != errorMsg(e2) {
				t.Fatalf("结果分歧 seed=%d step=%d", seed, step)
			}
			compareAfterOp(t, g, m, seed, step, now, e1)
		}
	}
}

func errorMsg(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

// compareAfterOp 对照内部状态；ErrClock 与参数非法不改变任何状态，无需比较。
func compareAfterOp(t *testing.T, g *Registry, m *sim, seed int64, step int, now int64, e error) {
	t.Helper()
	if errors.Is(e, ErrClock) || errors.Is(e, ErrArgument) {
		return
	}
	g.purgeExpired(now)
	m.purge(now)
	if g.nextRun != m.nextRun || g.clock != m.clock || len(g.live) != len(m.live) {
		t.Fatalf("状态分歧 seed=%d step=%d: run %d/%d clock %d/%d live %d/%d",
			seed, step, g.nextRun, m.nextRun, g.clock, m.clock, len(g.live), len(m.live))
	}
	for id, re := range g.live {
		me := m.live[id]
		if me == nil || re.run != me.run || re.state != me.state {
			t.Fatalf("记录分歧 seed=%d step=%d id=%s", seed, step, id)
		}
	}
}
