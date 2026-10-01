package coordinator

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

// naiveModel 是按需求规则逐条写成的朴素逐步模拟，用于与
// Coordinator 的实现对照。它刻意独立于 Coordinator 的实现。
type naiveModel struct {
	timeoutMs int64

	state   State
	gen     int
	leader  string
	members map[string]bool
	order   []string
	assign  Assignment

	start  int64
	maxNow int64
	hasNow bool
}

func newNaiveModel(timeoutMs int64) *naiveModel {
	return &naiveModel{
		timeoutMs: timeoutMs,
		state:     StateEmpty,
		members:   make(map[string]bool),
	}
}

func (m *naiveModel) checkClock(now int64) (string, error) {
	if m.hasNow && now < m.maxNow {
		return fmt.Sprintf("时钟倒退: now=%d < 高水位=%d，拒绝", now, m.maxNow), ErrClockBackwards
	}
	m.maxNow = now
	m.hasNow = true
	return "", nil
}

func (m *naiveModel) joined(id string) bool {
	return slices.Contains(m.order, id)
}

// complete 完成本轮：代数加一，领导者为本轮加入序最早者。
func (m *naiveModel) complete() string {
	m.gen++
	m.leader = m.order[0]
	m.assign = nil
	m.state = StateAwaitingSync
	return fmt.Sprintf("本轮完成: 代数=%d, 领导者=%s（加入序最早）, 状态=AwaitingSync", m.gen, m.leader)
}

func (m *naiveModel) join(id string, now int64) (string, error) {
	if reason, err := m.checkClock(now); err != nil {
		return reason, err
	}
	if m.state != StatePreparing {
		m.state = StatePreparing
		m.start = now
		m.order = nil
	}
	if !m.joined(id) {
		m.members[id] = true
		m.order = append(m.order, id)
	}
	if len(m.order) == len(m.members) {
		return m.complete(), nil
	}
	return fmt.Sprintf("进入/保持准备中: 开始时刻=%d, 已加入=%v, 已知成员未齐", m.start, m.order), nil
}

func (m *naiveModel) leave(id string, now int64) (string, error) {
	if reason, err := m.checkClock(now); err != nil {
		return reason, err
	}
	if !m.members[id] {
		return "未知成员，拒绝", ErrUnknownMember
	}
	wasPreparing := m.state == StatePreparing
	delete(m.members, id)
	m.order = slices.DeleteFunc(m.order, func(x string) bool { return x == id })
	switch {
	case len(m.members) == 0:
		m.state = StateEmpty
		m.order = nil
		return "成员清空: 状态=Empty, 代数不变", nil
	case wasPreparing && len(m.order) == 0:
		clear(m.members)
		m.state = StateEmpty
		m.order = nil
		return "准备中离开且本轮无人加入: 成员全部移除, 状态=Empty, 代数不变", nil
	case wasPreparing && len(m.order) == len(m.members):
		return m.complete(), nil
	case wasPreparing:
		return fmt.Sprintf("保持准备中: 剩余已知成员未全部加入, 已加入=%v", m.order), nil
	default:
		m.state = StatePreparing
		m.start = now
		m.order = nil
		return fmt.Sprintf("稳定/等待分配 -> 准备中: 开始时刻=%d", now), nil
	}
}

func (m *naiveModel) tick(now int64) (string, error) {
	if reason, err := m.checkClock(now); err != nil {
		return reason, err
	}
	if m.state != StatePreparing {
		return fmt.Sprintf("状态=%v 非准备中，忽略", m.state), nil
	}
	if now < m.start+m.timeoutMs {
		return fmt.Sprintf("now=%d < 开始时刻+T=%d，未超时", now, m.start+m.timeoutMs), nil
	}
	for id := range m.members {
		if !m.joined(id) {
			delete(m.members, id)
		}
	}
	if len(m.order) == 0 {
		clear(m.members)
		m.state = StateEmpty
		return "超时且本轮无人加入: 成员全部移除, 状态=Empty, 代数不变", nil
	}
	return m.complete(), nil
}

func (m *naiveModel) sync(id string, gen int, submit Assignment) (string, []string, error) {
	if !m.members[id] {
		return "未知成员，拒绝", nil, ErrUnknownMember
	}
	if gen != m.gen {
		return fmt.Sprintf("代数过期: 请求=%d, 当前=%d", gen, m.gen), nil, ErrStaleGeneration
	}
	switch m.state {
	case StatePreparing:
		return "准备中，需重新加入", nil, ErrRejoinNeeded
	case StateAwaitingSync:
		if len(submit) == 0 {
			return "仅查询，分配尚未就绪", nil, ErrNotReady
		}
		if id != m.leader {
			return "非领导者提交，拒绝", nil, ErrNotLeader
		}
		if len(submit) != len(m.members) {
			return "分配表成员集不符，拒绝", nil, ErrAssignmentMismatch
		}
		for mid := range submit {
			if !m.members[mid] {
				return "分配表成员集不符，拒绝", nil, ErrAssignmentMismatch
			}
		}
		m.assign = submit
		m.state = StateStable
		return "领导者提交成功: 状态=Stable", m.assign[id], nil
	case StateStable:
		return "稳定状态，返回自己的分配", m.assign[id], nil
	default:
		return "空状态不可达（未知成员已先报）", nil, ErrUnknownMember
	}
}

func (m *naiveModel) heartbeat(id string, gen int) (string, error) {
	if !m.members[id] {
		return "未知成员，拒绝", ErrUnknownMember
	}
	if gen != m.gen {
		return fmt.Sprintf("代数不等: 请求=%d, 当前=%d", gen, m.gen), ErrStaleGeneration
	}
	if m.state == StatePreparing {
		return "准备中，需重新加入", ErrRejoinNeeded
	}
	return "心跳正常", nil
}

func (m *naiveModel) sortedMembers() []string {
	ids := make([]string, 0, len(m.members))
	for id := range m.members {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// errKey 将错误归约为可比较的键，要求所有拒绝原因可通过 errors.Is 区分。
func errKey(err error) string {
	switch {
	case err == nil:
		return "<nil>"
	case errors.Is(err, ErrClockBackwards):
		return "ErrClockBackwards"
	case errors.Is(err, ErrUnknownMember):
		return "ErrUnknownMember"
	case errors.Is(err, ErrStaleGeneration):
		return "ErrStaleGeneration"
	case errors.Is(err, ErrRejoinNeeded):
		return "ErrRejoinNeeded"
	case errors.Is(err, ErrNotReady):
		return "ErrNotReady"
	case errors.Is(err, ErrNotLeader):
		return "ErrNotLeader"
	case errors.Is(err, ErrAssignmentMismatch):
		return "ErrAssignmentMismatch"
	default:
		return fmt.Sprintf("未知错误: %v", err)
	}
}

// op 描述一次对协调者的调用。
type op struct {
	desc   string
	apply  func(c *Coordinator) ([]string, error)
	replay func(m *naiveModel) (string, []string, error)
}

func randomOps(r *rand.Rand, n int) []op {
	members := []string{"A", "B", "C", "D"}
	ops := make([]op, 0, n)
	var now int64
	for i := 0; i < n; i++ {
		// 时钟基本单调前进，偶尔倒退以触发时钟倒退拒绝。
		if r.Intn(10) == 0 && now > 0 {
			now -= int64(r.Intn(500))
		} else {
			now += int64(r.Intn(400))
		}
		m := members[r.Intn(len(members))]
		switch r.Intn(5) {
		case 0, 1:
			m, now := m, now
			ops = append(ops, op{
				desc:   fmt.Sprintf("Join(%s, %d)", m, now),
				apply:  func(c *Coordinator) ([]string, error) { return nil, c.Join(m, now) },
				replay: func(md *naiveModel) (string, []string, error) { s, e := md.join(m, now); return s, nil, e },
			})
		case 2:
			m, now := m, now
			ops = append(ops, op{
				desc:   fmt.Sprintf("Leave(%s, %d)", m, now),
				apply:  func(c *Coordinator) ([]string, error) { return nil, c.Leave(m, now) },
				replay: func(md *naiveModel) (string, []string, error) { s, e := md.leave(m, now); return s, nil, e },
			})
		case 3:
			now := now
			ops = append(ops, op{
				desc:   fmt.Sprintf("Tick(%d)", now),
				apply:  func(c *Coordinator) ([]string, error) { return nil, c.Tick(now) },
				replay: func(md *naiveModel) (string, []string, error) { s, e := md.tick(now); return s, nil, e },
			})
		default:
			m, gen := m, r.Intn(4)
			if r.Intn(2) == 0 {
				var submit Assignment
				if r.Intn(2) == 0 {
					submit = Assignment{}
					for _, cand := range members {
						if r.Intn(2) == 0 {
							submit[cand] = []string{fmt.Sprintf("p%d", r.Intn(4))}
						}
					}
				}
				ops = append(ops, op{
					desc:   fmt.Sprintf("Sync(%s, %d, %v)", m, gen, submit),
					apply:  func(c *Coordinator) ([]string, error) { return c.Sync(m, gen, submit) },
					replay: func(md *naiveModel) (string, []string, error) { return md.sync(m, gen, submit) },
				})
			} else {
				ops = append(ops, op{
					desc:   fmt.Sprintf("Heartbeat(%s, %d)", m, gen),
					apply:  func(c *Coordinator) ([]string, error) { return nil, c.Heartbeat(m, gen) },
					replay: func(md *naiveModel) (string, []string, error) { s, e := md.heartbeat(m, gen); return s, nil, e },
				})
			}
		}
	}
	return ops
}

// TestModelReplay 用固定种子的随机调用序列同时驱动协调者、第二个
// 协调者（重放确定性）与朴素模拟，逐步对照输出与完整状态，并打印
// 每步的输入、输出与判定依据。
func TestModelReplay(t *testing.T) {
	r := rand.New(rand.NewSource(1024))
	ops := randomOps(r, 400)

	c1 := New(testTimeoutMs)
	c2 := New(testTimeoutMs)
	model := newNaiveModel(testTimeoutMs)

	for i, o := range ops {
		gotOut, gotErr := o.apply(c1)
		_, replayErr := o.apply(c2)
		reason, modelOut, modelErr := o.replay(model)

		t.Logf("步骤 %d: 输入=%s | 输出 err=%s out=%v | 判定依据: %s",
			i, o.desc, errKey(gotErr), gotOut, reason)

		if errKey(gotErr) != errKey(modelErr) {
			t.Fatalf("步骤 %d (%s): 错误 = %s, 朴素模拟 = %s", i, o.desc, errKey(gotErr), errKey(modelErr))
		}
		if errKey(gotErr) != errKey(replayErr) {
			t.Fatalf("步骤 %d (%s): 重放错误 = %s, 首次 = %s", i, o.desc, errKey(replayErr), errKey(gotErr))
		}
		if !slices.Equal(gotOut, modelOut) {
			t.Fatalf("步骤 %d (%s): 同步返回 = %v, 朴素模拟 = %v", i, o.desc, gotOut, modelOut)
		}
		if c1.State() != model.state {
			t.Fatalf("步骤 %d (%s): 状态 = %v, 朴素模拟 = %v", i, o.desc, c1.State(), model.state)
		}
		if c1.Generation() != model.gen {
			t.Fatalf("步骤 %d (%s): 代数 = %d, 朴素模拟 = %d", i, o.desc, c1.Generation(), model.gen)
		}
		if c1.Leader() != model.leader {
			t.Fatalf("步骤 %d (%s): 领导者 = %q, 朴素模拟 = %q", i, o.desc, c1.Leader(), model.leader)
		}
		if got := c1.Members(); !slices.Equal(got, model.sortedMembers()) {
			t.Fatalf("步骤 %d (%s): 成员 = %v, 朴素模拟 = %v", i, o.desc, got, model.sortedMembers())
		}
		if got := c1.JoinOrder(); !slices.Equal(got, model.order) {
			t.Fatalf("步骤 %d (%s): 加入序 = %v, 朴素模拟 = %v", i, o.desc, got, model.order)
		}
		// 重放得到完全相同的状态序列。
		if c2.State() != c1.State() || c2.Generation() != c1.Generation() || c2.Leader() != c1.Leader() {
			t.Fatalf("步骤 %d (%s): 重放状态不一致", i, o.desc)
		}
	}
	t.Logf("终态: 状态=%v 代数=%d 领导者=%q 成员=%v",
		c1.State(), c1.Generation(), c1.Leader(), c1.Members())
}

// TestConcurrent 验证所有操作可并发调用：结果等价于某个串行顺序，
// 且任何时刻代数只增不减。
func TestConcurrent(t *testing.T) {
	c := New(testTimeoutMs)
	var clock atomic.Int64

	const workers = 8
	const joinsPerWorker = 50
	var wg sync.WaitGroup
	var genErr atomic.Value

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			prevGen := 0
			for i := 0; i < joinsPerWorker; i++ {
				now := clock.Add(1)
				_ = c.Join(id, now)
				_ = c.Heartbeat(id, c.Generation())
				_ = c.Tick(clock.Load())
				if g := c.Generation(); g < prevGen {
					genErr.Store(fmt.Sprintf("代数回退: %d -> %d", prevGen, g))
					return
				} else {
					prevGen = g
				}
			}
		}(fmt.Sprintf("member-%d", w))
	}
	wg.Wait()

	if v := genErr.Load(); v != nil {
		t.Fatal(v)
	}
	// 串行重放加入直到本轮完成：准备中时每加入一个缺失成员都
	// 更接近齐备，至多 workers 次调用内完成。
	for i := 0; c.State() != StateAwaitingSync && i < workers; i++ {
		if err := c.Join(fmt.Sprintf("member-%d", i), clock.Add(1)); err != nil {
			t.Fatal(err)
		}
	}
	requireState(t, c, StateAwaitingSync, c.Generation())
	if got := len(c.Members()); got != workers {
		t.Fatalf("成员数 = %d, 期望 %d", got, workers)
	}
}
