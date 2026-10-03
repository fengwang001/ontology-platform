// Package saga 管理 Saga 实例：先写日志、后登记副作用。
package saga

import (
	"errors"
	"fmt"
	"sync"

	"ontology/effect"
	"ontology/journal"
)

// 四类拒绝原因，可用 errors.Is 区分；
// 检查优先级：参数非法 > 实例已存在/不存在 > 状态不符 > 步骤不符。
var (
	ErrInvalidArg = errors.New("参数非法")
	ErrInstance   = errors.New("实例已存在或不存在")
	ErrState      = errors.New("状态不符")
	ErrStep       = errors.New("步骤不符")
)

const (
	maxSteps  = 64
	maxBudget = 10
)

// instance 为单实例状态；日志是真相，内存只保存“在途”位。
type instance struct {
	mu           sync.Mutex
	n, p, budget int
	inFlight     bool // 最后一条 I/CI 是否在途
	flightStep   int
	flightComp   bool // true 表示 CI 在途，false 表示 I 在途
}

// Saga 为实例管理器。
type Saga struct {
	jr   *journal.Journal
	eff  *effect.Table
	mu   sync.Mutex // 仅保护 inst 表
	inst map[string]*instance
}

// New 以给定日志与幂等表构造管理器。
func New(jr *journal.Journal, eff *effect.Table) *Saga {
	return &Saga{jr: jr, eff: eff, inst: make(map[string]*instance)}
}

// Begin 创建实例并写入 B 记录；id 非空，1≤n≤64，0≤p<n，0≤B≤10。
func (s *Saga) Begin(id []byte, n, p, budget int) error {
	if len(id) == 0 || n < 1 || n > maxSteps || p < 0 || p >= n || budget < 0 || budget > maxBudget {
		return fmt.Errorf("%w: Begin(idLen=%d, n=%d, p=%d, B=%d)", ErrInvalidArg, len(id), n, p, budget)
	}
	key := string(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.inst[key]; ok {
		return fmt.Errorf("%w: 重复 Begin %q", ErrInstance, key)
	}
	s.inst[key] = &instance{n: n, p: p, budget: budget}
	s.jr.Append(key, journal.Record{Kind: journal.KindBegin})
	return nil
}

// lookup 校验 id 非空并取出实例。
func (s *Saga) lookup(id []byte) (string, *instance, error) {
	if len(id) == 0 {
		return "", nil, fmt.Errorf("%w: id 为空", ErrInvalidArg)
	}
	key := string(id)
	s.mu.Lock()
	inst, ok := s.inst[key]
	s.mu.Unlock()
	if !ok {
		return "", nil, fmt.Errorf("%w: %q 不存在", ErrInstance, key)
	}
	return key, inst, nil
}

// checkStep 校验下标在 [0,n) 内（需先拿到实例）。
func checkStep(inst *instance, i int) error {
	if i >= inst.n {
		return fmt.Errorf("%w: 下标 %d 越界（n=%d）", ErrInvalidArg, i, inst.n)
	}
	return nil
}

// checkStepGlobal 不依赖实例的下标粗检，优先于实例存在性检查。
func checkStepGlobal(i int) error {
	if i < 0 || i >= maxSteps {
		return fmt.Errorf("%w: 下标 %d 越界", ErrInvalidArg, i)
	}
	return nil
}

// PlanKind 为恢复/推进计划的种类。
type PlanKind int

const (
	Forward     PlanKind = iota // 前滚 Step
	Compensate                  // 补偿 Step
	ProbePivot                  // 探测枢轴
	Completed                   // 已完成
	Compensated                 // 已补偿
	Manual                      // 转人工
)

// Plan 描述下一步计划；Step 仅对 Forward/Compensate 有效，其余为 -1。
type Plan struct {
	Kind PlanKind
	Step int
}

func (k PlanKind) String() string {
	switch k {
	case Forward:
		return "前滚"
	case Compensate:
		return "补偿"
	case ProbePivot:
		return "探测"
	case Completed:
		return "已完成"
	case Compensated:
		return "已补偿"
	case Manual:
		return "转人工"
	}
	return "未知"
}

func (pl Plan) String() string {
	if pl.Kind == Forward || pl.Kind == Compensate {
		return fmt.Sprintf("%s(%d)", pl.Kind, pl.Step)
	}
	return pl.Kind.String()
}

// planOf 只由日志前缀推出恢复计划，不看内存中的在途位。
func planOf(recs []journal.Record, n, p, budget int) Plan {
	last := recs[len(recs)-1]
	switch last.Kind {
	case journal.KindBegin:
		return Plan{Kind: Forward, Step: 0}
	case journal.KindDone:
		if last.Step == n-1 {
			return Plan{Kind: Completed, Step: -1}
		}
		return Plan{Kind: Forward, Step: last.Step + 1}
	case journal.KindIntent: // 结果未知
		if last.Step < p {
			return Plan{Kind: Compensate, Step: last.Step} // 含 i 本身
		}
		if last.Step == p {
			return Plan{Kind: ProbePivot, Step: -1}
		}
		return Plan{Kind: Forward, Step: last.Step} // 重做，副作用幂等
	case journal.KindFail:
		c := 0
		for _, r := range recs {
			if r.Kind == journal.KindFail && r.Step == last.Step && r.Fail == journal.Transient {
				c++
			}
		}
		terminal := last.Fail == journal.Permanent || (last.Step <= p && c > budget)
		if !terminal {
			return Plan{Kind: Forward, Step: last.Step}
		}
		if last.Step > p {
			return Plan{Kind: Manual, Step: -1}
		}
		if last.Step == 0 {
			return Plan{Kind: Compensated, Step: -1}
		}
		return Plan{Kind: Compensate, Step: last.Step - 1} // 失败步骤本身不补偿
	case journal.KindCompIntent:
		return Plan{Kind: Compensate, Step: last.Step} // 重做
	case journal.KindCompDone:
		if last.Step == 0 {
			return Plan{Kind: Compensated, Step: -1}
		}
		return Plan{Kind: Compensate, Step: last.Step - 1}
	}
	panic("planOf: 未知记录")
}
