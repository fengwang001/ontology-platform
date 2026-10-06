package loto

import (
	"sort"
	"strconv"
	"sync"
)

// System 内部状态与索引。
//
// 关键索引（保证送电/冲突判定不随历史票数增长）：
//
//	activeByDevice: 设备 -> 当前处于占用态（生效至完成之间）的票集合；
//	  票一生效即加入，一完成即摘除。历史完成票不在其中。
//	lockOwners:     隔离点 -> 当前物理在位的锁集合（票内每人一把）。
type realState struct {
	persons map[string]*Person
	devices map[string]map[string]bool // 设备 -> 依赖隔离点集合
	permits map[string]*Permit

	activeByDevice map[string]map[string]bool // deviceID -> permitID set
	lockOwners     map[string]map[string]bool // pointID -> "permit\x00worker" set

	clock int64
	audit []AuditEntry
	seq   int64
}

func newState() *realState {
	return &realState{
		persons:        map[string]*Person{},
		devices:        map[string]map[string]bool{},
		permits:        map[string]*Permit{},
		activeByDevice: map[string]map[string]bool{},
		lockOwners:     map[string]map[string]bool{},
	}
}

// System 是能量隔离上锁挂牌工作票系统。
// 所有方法都可被并发调用；内部以单一互斥量串行化，
// 因而任意并发执行等价于某个合法的串行顺序。
type System struct {
	mu sync.Mutex
	st *realState
}

// New 创建空系统。
func New() *System {
	return &System{st: newState()}
}

func lockKey(permit, worker string) string { return permit + "\x00" + worker }

func (s *System) log(at int64, op, actor, permit, detail string) {
	s.st.seq++
	s.st.audit = append(s.st.audit, AuditEntry{
		Seq: s.st.seq, Time: at, Op: op, Actor: actor, Permit: permit, Detail: detail,
	})
}

// checkClock 只校验时刻，不改变任何状态（被拒绝的操作不得推进时钟）。
func (s *System) checkClock(at int64) error {
	if at < 0 {
		return fail(InvalidParam, "time must be non-negative, got %d", at)
	}
	if at < s.st.clock {
		return fail(ClockRollback, "time %d < last accepted time %d", at, s.st.clock)
	}
	return nil
}

// advanceClock 在操作确定将被接受后调用：标记逾期并提交时钟。
func (s *System) advanceClock(at int64) {
	for id, p := range s.st.permits {
		if !p.Overdue && p.Phase != PhasePending && p.Phase != PhaseCompleted && at >= p.End {
			p.Overdue = true
			s.log(at, "auto_overdue", "", id,
				"reached planned end "+strconv.FormatInt(p.End, 10)+" without completion")
		}
	}
	s.st.clock = at
}

// occupied：票是否处于占用态（已生效至完成之间）。
func (p *Permit) occupied() bool {
	return p.Phase == PhaseEffective || p.Phase == PhaseVerified ||
		p.Phase == PhaseWorking || p.Phase == PhaseTrial
}

// physicalLocksOn 返回某隔离点当前物理在位的锁（用于送电判定）。
func (s *System) physicalLocksOn(point string) []string {
	set := s.st.lockOwners[point]
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
