package group

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// sim 是按规则逐步写成的朴素模拟：切片存储、线性查找，与协调者实现
// 相互独立，用于逐操作对照状态、输出与判定依据。
type sim struct {
	timeout int64
	state   State
	gen     int
	members []string
	joinSeq []string
	leader  string
	start   int64
	assign  Assignment
	lastNow int64
	hasNow  bool
}

type simOut struct {
	own    []string
	err    error
	reason string
}

func newSim(timeout int64) *sim {
	return &sim{timeout: timeout, state: StateEmpty}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func removeStr(s []string, v string) []string {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func (s *sim) clock(now int64) error {
	if s.hasNow && now < s.lastNow {
		return ErrClockBackwards
	}
	s.lastNow, s.hasNow = now, true
	return nil
}

func (s *sim) allJoined() bool {
	if len(s.members) == 0 {
		return false
	}
	for _, m := range s.members {
		if !contains(s.joinSeq, m) {
			return false
		}
	}
	return true
}

func (s *sim) complete() {
	s.gen++
	s.leader = s.joinSeq[0]
	s.state = StateAwaitingAssignment
	s.assign = nil
}

func (s *sim) toEmpty() {
	s.state = StateEmpty
	s.members = nil
	s.joinSeq = nil
	s.leader = ""
	s.assign = nil
}

func (s *sim) join(member string, now int64) simOut {
	if err := s.clock(now); err != nil {
		return simOut{err: err, reason: fmt.Sprintf("now=%d 小于此前最大 now=%d，时钟倒退", now, s.lastNow)}
	}
	var reason string
	switch s.state {
	case StateEmpty, StateStable, StateAwaitingAssignment:
		s.state = StatePreparing
		s.start = now
		s.assign = nil
		s.joinSeq = nil
		if !contains(s.members, member) {
			s.members = append(s.members, member)
		}
		s.joinSeq = append(s.joinSeq, member)
		reason = fmt.Sprintf("非准备中状态下加入，进入准备中，开始时刻=%d，%s 记入本轮加入序", now, member)
	case StatePreparing:
		switch {
		case contains(s.joinSeq, member):
			reason = fmt.Sprintf("%s 本轮已加入，重复加入幂等，次序与开始时刻不变", member)
		default:
			if !contains(s.members, member) {
				s.members = append(s.members, member)
			}
			s.joinSeq = append(s.joinSeq, member)
			reason = fmt.Sprintf("%s 本轮首次加入，追加到加入序末尾，开始时刻不变", member)
		}
	}
	if s.state == StatePreparing && s.allJoined() {
		s.complete()
		reason += fmt.Sprintf("；所有已知成员都已在本轮加入，立即完成：代数+1=%d，领导者=本轮最早加入者 %s，进入等待分配", s.gen, s.leader)
	}
	return simOut{reason: reason}
}

func (s *sim) leave(member string, now int64) simOut {
	if err := s.clock(now); err != nil {
		return simOut{err: err, reason: fmt.Sprintf("now=%d 小于此前最大 now=%d，时钟倒退", now, s.lastNow)}
	}
	if !contains(s.members, member) {
		return simOut{reason: fmt.Sprintf("%s 不是已知成员，离开幂等为空操作", member)}
	}
	s.members = removeStr(s.members, member)
	s.joinSeq = removeStr(s.joinSeq, member)
	var reason string
	switch s.state {
	case StateStable, StateAwaitingAssignment:
		s.assign = nil
		if len(s.members) == 0 {
			s.toEmpty()
			reason = "成员清空，进入空状态，代数不变"
		} else {
			s.state = StatePreparing
			s.start = now
			s.joinSeq = nil
			reason = fmt.Sprintf("稳定/等待分配下离开，进入准备中，开始时刻=%d", now)
		}
	case StatePreparing:
		switch {
		case len(s.members) == 0:
			s.toEmpty()
			reason = "成员清空，进入空状态，代数不变"
		case len(s.joinSeq) == 0:
			s.toEmpty()
			reason = "准备中离开后本轮已无任何加入，成员全部移除，进入空状态，代数不变"
		case s.allJoined():
			s.complete()
			reason = fmt.Sprintf("剩余已知成员都已加入，立即完成：代数+1=%d，领导者=%s", s.gen, s.leader)
		default:
			reason = "准备中移除后仍有成员未加入，继续等待"
		}
	}
	return simOut{reason: reason}
}

func (s *sim) tick(now int64) simOut {
	if err := s.clock(now); err != nil {
		return simOut{err: err, reason: fmt.Sprintf("now=%d 小于此前最大 now=%d，时钟倒退", now, s.lastNow)}
	}
	if s.state != StatePreparing {
		return simOut{reason: "非准备中，Tick 为空操作"}
	}
	if now < s.start+s.timeout {
		return simOut{reason: fmt.Sprintf("now=%d 小于开始时刻+T=%d，未超时", now, s.start+s.timeout)}
	}
	if len(s.joinSeq) == 0 {
		s.toEmpty()
		return simOut{reason: "超时且本轮无任何加入，成员全部移除，进入空状态，代数不变"}
	}
	s.members = append([]string(nil), s.joinSeq...)
	s.complete()
	return simOut{reason: fmt.Sprintf("now=%d 不小于开始时刻+T，超时完成：未加入者被移除，代数+1=%d，领导者=%s", now, s.gen, s.leader)}
}

func (s *sim) syncOp(member string, gen int, assign Assignment) simOut {
	if !contains(s.members, member) {
		return simOut{err: ErrUnknownMember, reason: "成员未知"}
	}
	if gen != s.gen {
		return simOut{err: ErrStaleGeneration, reason: fmt.Sprintf("请求代数=%d 不等于当前代数=%d", gen, s.gen)}
	}
	switch s.state {
	case StatePreparing:
		return simOut{err: ErrRejoinNeeded, reason: "准备中，需重新加入"}
	case StateAwaitingAssignment:
		if len(assign) == 0 {
			return simOut{err: ErrNotReady, reason: "等待分配状态下的空表查询，尚未就绪"}
		}
		if member != s.leader {
			return simOut{err: ErrNotLeader, reason: "非领导者提交非空分配表"}
		}
		if !simSameSet(assign, s.members) {
			return simOut{err: ErrAssignmentMismatch, reason: "分配表成员集合与本代成员集合不一致"}
		}
		s.assign = assign
		s.state = StateStable
		return simOut{own: assign[member], reason: "领导者提交成员集恰等的分配表，进入稳定"}
	default: // StateStable；StateEmpty 时成员必未知，不可达
		return simOut{own: s.assign[member], reason: "稳定状态，返回自己的分配"}
	}
}

func (s *sim) heartbeat(member string, gen int) simOut {
	if !contains(s.members, member) {
		return simOut{err: ErrUnknownMember, reason: "成员未知"}
	}
	if gen != s.gen {
		return simOut{err: ErrStaleGeneration, reason: fmt.Sprintf("请求代数=%d 不等于当前代数=%d", gen, s.gen)}
	}
	if s.state == StatePreparing {
		return simOut{err: ErrRejoinNeeded, reason: "准备中，需重新加入"}
	}
	return simOut{reason: "心跳正常"}
}

func simSameSet(a Assignment, members []string) bool {
	if len(a) != len(members) {
		return false
	}
	for _, m := range members {
		if _, ok := a[m]; !ok {
			return false
		}
	}
	return true
}

func (s *sim) sortedMembers() []string {
	out := append([]string(nil), s.members...)
	sort.Strings(out)
	return out
}

// op 是一次对协调者的调用。
type op struct {
	kind   string // join / leave / tick / sync / heartbeat
	member string
	now    int64
	gen    int
	assign Assignment
}

func (o op) String() string {
	switch o.kind {
	case "sync":
		return fmt.Sprintf("Sync(%q, gen=%d, assign=%v)", o.member, o.gen, o.assign)
	case "heartbeat":
		return fmt.Sprintf("Heartbeat(%q, gen=%d)", o.member, o.gen)
	default:
		return fmt.Sprintf("%s(%q, now=%d)", o.kind, o.member, o.now)
	}
}

func applyOp(c *Coordinator, o op) ([]string, error) {
	switch o.kind {
	case "join":
		return nil, c.Join(o.member, o.now)
	case "leave":
		return nil, c.Leave(o.member, o.now)
	case "tick":
		return nil, c.Tick(o.now)
	case "sync":
		return c.Sync(o.member, o.gen, o.assign)
	case "heartbeat":
		return nil, c.Heartbeat(o.member, o.gen)
	default:
		panic("unknown op " + o.kind)
	}
}

func applySim(s *sim, o op) simOut {
	switch o.kind {
	case "join":
		return s.join(o.member, o.now)
	case "leave":
		return s.leave(o.member, o.now)
	case "tick":
		return s.tick(o.now)
	case "sync":
		return s.syncOp(o.member, o.gen, o.assign)
	case "heartbeat":
		return s.heartbeat(o.member, o.gen)
	default:
		panic("unknown op " + o.kind)
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b) && errors.Is(b, a)
}

func equalSlice(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// runOps 将操作序列同时应用到协调者、重放的第二个协调者与朴素模拟，
// 逐步对照输出与完整状态，并打印输入、输出与判定依据。
func runOps(t *testing.T, ops []op) {
	t.Helper()
	c := New(testTimeoutMs)
	replay := New(testTimeoutMs)
	s := newSim(testTimeoutMs)
	prevGen := 0
	for i, o := range ops {
		own, err := applyOp(c, o)
		repOwn, repErr := applyOp(replay, o)
		want := applySim(s, o)

		t.Logf("step %03d 输入: %s", i, o)
		t.Logf("         输出: own=%v err=%v", own, err)
		t.Logf("         判定依据: %s", want.reason)
		t.Logf("         状态: %s gen=%d leader=%q members=%v joinOrder=%v",
			s.state, s.gen, s.leader, s.sortedMembers(), s.joinSeq)

		if !sameErr(err, want.err) {
			t.Fatalf("step %d %s: err = %v, 模拟期望 %v", i, o, err, want.err)
		}
		if !equalSlice(own, want.own) {
			t.Fatalf("step %d %s: own = %v, 模拟期望 %v", i, o, own, want.own)
		}
		// 重放同一序列必须得到完全相同的输出。
		if !sameErr(repErr, err) || !equalSlice(repOwn, own) {
			t.Fatalf("step %d %s: 重放输出 (%v, %v) 与首次 (%v, %v) 不一致", i, o, repOwn, repErr, own, err)
		}
		// 与模拟对照完整状态。
		assertAgainst(t, i, c, s)
		assertAgainst(t, i, replay, s)
		// 不变量：代数只增不减。
		if g := c.Generation(); g < prevGen {
			t.Fatalf("step %d: 代数从 %d 回退到 %d", i, prevGen, g)
		} else {
			prevGen = g
		}
		// 不变量：稳定状态下每个成员都能查到自己的分配。
		if c.State() == StateStable {
			for _, m := range c.Members() {
				if _, err := c.Sync(m, c.Generation(), nil); err != nil {
					t.Fatalf("step %d: 稳定状态下成员 %s 查询分配失败: %v", i, m, err)
				}
			}
		}
	}
}

func assertAgainst(t *testing.T, step int, c *Coordinator, s *sim) {
	t.Helper()
	if c.State() != s.state {
		t.Fatalf("step %d: state = %v, 模拟 %v", step, c.State(), s.state)
	}
	if c.Generation() != s.gen {
		t.Fatalf("step %d: gen = %d, 模拟 %d", step, c.Generation(), s.gen)
	}
	if c.Leader() != s.leader {
		t.Fatalf("step %d: leader = %q, 模拟 %q", step, c.Leader(), s.leader)
	}
	if got, want := c.Members(), s.sortedMembers(); !equalSlice(got, want) {
		t.Fatalf("step %d: members = %v, 模拟 %v", step, got, want)
	}
	if got, want := c.JoinOrder(), s.joinSeq; !equalSlice(got, want) {
		t.Fatalf("step %d: joinOrder = %v, 模拟 %v", step, got, want)
	}
}

// TestScriptedSequenceAgainstSim 用覆盖全部状态迁移与拒绝路径的脚本化
// 序列与朴素模拟逐步对照。
func TestScriptedSequenceAgainstSim(t *testing.T) {
	ops := []op{
		{kind: "join", member: "A", now: 0},                                               // 空 -> 立即完成，gen=1
		{kind: "heartbeat", member: "A", gen: 0},                                          // 代数过期
		{kind: "heartbeat", member: "A", gen: 1},                                          // 正常
		{kind: "sync", member: "A", gen: 1},                                               // 等待分配，领导者查询：尚未就绪
		{kind: "sync", member: "A", gen: 1, assign: Assignment{"A": {"p0"}}},              // 进入稳定
		{kind: "sync", member: "A", gen: 1},                                               // 稳定，返回自己的分配
		{kind: "join", member: "B", now: 100},                                             // 稳定 -> 准备中
		{kind: "join", member: "B", now: 200},                                             // 重复加入，幂等
		{kind: "heartbeat", member: "A", gen: 1},                                          // 准备中：需重新加入
		{kind: "sync", member: "ghost", gen: 1},                                           // 未知成员
		{kind: "join", member: "A", now: 300},                                             // 全员到齐，立即完成，gen=2，领导者 B
		{kind: "sync", member: "A", gen: 2, assign: Assignment{"A": nil, "B": nil}},       // 非领导者提交
		{kind: "sync", member: "B", gen: 2, assign: Assignment{"A": nil}},                 // 成员集不符
		{kind: "sync", member: "B", gen: 2, assign: Assignment{"A": {"p0"}, "B": {"p1"}}}, // 稳定
		{kind: "leave", member: "A", now: 400},                                            // 稳定 -> 准备中
		{kind: "tick", now: 400 + testTimeoutMs},                                          // 恰等于开始+T，本轮无加入，清空
		{kind: "join", member: "C", now: 2000},                                            // 空 -> gen=3
		{kind: "join", member: "D", now: 2100},                                            // 等待分配 -> 准备中
		{kind: "leave", member: "C", now: 2200},                                           // 剩余 {D} 已齐备，立即完成，gen=4
		{kind: "join", member: "A", now: 2300},                                            // 等待分配 -> 准备中
		{kind: "join", member: "B", now: 2400},                                            // 准备中，加入序 [A, B]
		{kind: "tick", now: 2400 + testTimeoutMs},                                         // 超时完成，D 被移除，gen=5，领导者 A
		{kind: "join", member: "B", now: 4000},                                            // 已知成员再次加入 -> 准备中
		{kind: "tick", now: 4999},                                                         // 未超时
		{kind: "tick", now: 5000},                                                         // 恰等于开始+T，完成，gen=6
		{kind: "join", member: "X", now: 4999},                                            // 时钟倒退
		{kind: "leave", member: "ghost", now: 5000},                                       // 未知成员离开，幂等
	}
	runOps(t, ops)
}

// TestRandomSequenceAgainstSim 用固定种子的随机操作序列与朴素模拟对照，
// 重放可复现。
func TestRandomSequenceAgainstSim(t *testing.T) {
	for _, seed := range []int64{1, 7, 42} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			s := newSim(testTimeoutMs)
			members := []string{"A", "B", "C", "D", "E"}
			var now int64
			var ops []op
			for i := 0; i < 400; i++ {
				now += int64(r.Intn(1600)) // 可能原地不动，也可能越过 T
				member := members[r.Intn(len(members))]
				if r.Intn(50) == 0 {
					member = "ghost" // 偶发未知成员
				}
				opNow := now
				if r.Intn(50) == 0 && now > 0 {
					opNow = now - 1 - int64(r.Intn(10)) // 偶发时钟倒退
				}
				var o op
				switch r.Intn(5) {
				case 0:
					o = op{kind: "join", member: member, now: opNow}
				case 1:
					o = op{kind: "leave", member: member, now: opNow}
				case 2:
					o = op{kind: "tick", now: opNow}
				case 3:
					o = op{kind: "sync", member: member, gen: s.gen + r.Intn(3) - 1}
					if r.Intn(2) == 0 {
						o.assign = randomAssignment(r, s)
					}
				case 4:
					o = op{kind: "heartbeat", member: member, gen: s.gen + r.Intn(3) - 1}
				}
				ops = append(ops, o)
				applySim(s, o) // 推进模拟以指导下一次生成
			}
			runOps(t, ops)
		})
	}
}

// randomAssignment 多数时候生成成员集恰等的分配表，偶尔多/少成员。
func randomAssignment(r *rand.Rand, s *sim) Assignment {
	a := Assignment{}
	for _, m := range s.members {
		if r.Intn(10) == 0 {
			continue // 偶发缺成员
		}
		a[m] = []string{fmt.Sprintf("p%d", r.Intn(4))}
	}
	if r.Intn(10) == 0 {
		a["ghost"] = nil // 偶发多成员
	}
	return a
}

// TestConcurrentOps 并发调用各操作，验证结果等价于某个串行顺序：
// 无数据竞争（配合 -race）、代数只增不减、稳定状态分配可查。
func TestConcurrentOps(t *testing.T) {
	c := New(50)
	var clock atomic.Int64
	var wg sync.WaitGroup
	stop := make(chan struct{})
	sampleDone := make(chan struct{})
	go func() {
		defer close(sampleDone)
		prev := 0
		for {
			select {
			case <-stop:
				return
			default:
				if g := c.Generation(); g < prev {
					t.Errorf("代数从 %d 回退到 %d", prev, g)
				} else {
					prev = g
				}
			}
		}
	}()
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id) + 1))
			member := fmt.Sprintf("m%d", id%4)
			for i := 0; i < 300; i++ {
				now := clock.Add(1)
				switch r.Intn(5) {
				case 0:
					_ = c.Join(member, now)
				case 1:
					_ = c.Leave(member, now)
				case 2:
					_ = c.Tick(now)
				case 3:
					_ = c.Heartbeat(member, c.Generation())
				case 4:
					gen := c.Generation()
					_, _ = c.Sync(member, gen, nil)
					_, _ = c.Sync(member, gen, Assignment{member: {"p0"}})
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	<-sampleDone
	if c.State() == StateStable {
		for _, m := range c.Members() {
			if _, err := c.Sync(m, c.Generation(), nil); err != nil {
				t.Fatalf("稳定状态下成员 %s 查询分配失败: %v", m, err)
			}
		}
	}
	t.Logf("终态: %s gen=%d leader=%q members=%v", c.State(), c.Generation(), c.Leader(), c.Members())
}
