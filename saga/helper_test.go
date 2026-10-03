package saga_test

import (
	"testing"

	"ontology/journal"
	"ontology/saga"
)

// sim 是按规则逐步推进的朴素模拟器：逐条消费日志记录，
// 瞬时失败计数随记录增量累加，与 saga 包内“看最后一条+全量重扫”
// 的实现相互独立，用于交叉对照。
type sim struct {
	n, p, b int
	trans   []int
	plan    saga.Plan
}

func newSim(n, p, b int) *sim { return &sim{n: n, p: p, b: b, trans: make([]int, n)} }

func (s *sim) step(r journal.Record) {
	switch r.Kind {
	case journal.KindBegin:
		s.plan = saga.Plan{Kind: saga.PlanForward, Step: 0}
	case journal.KindIntent:
		switch i := r.Step; {
		case i < s.p:
			s.plan = saga.Plan{Kind: saga.PlanCompensate, Step: i}
		case i == s.p:
			s.plan = saga.Plan{Kind: saga.PlanProbe}
		default:
			s.plan = saga.Plan{Kind: saga.PlanForward, Step: i}
		}
	case journal.KindDone:
		if r.Step == s.n-1 {
			s.plan = saga.Plan{Kind: saga.PlanFinished}
		} else {
			s.plan = saga.Plan{Kind: saga.PlanForward, Step: r.Step + 1}
		}
	case journal.KindFail:
		i := r.Step
		if r.Fail == journal.Transient {
			s.trans[i]++
		}
		terminal := r.Fail == journal.Permanent || (i <= s.p && s.trans[i] > s.b)
		switch {
		case !terminal:
			s.plan = saga.Plan{Kind: saga.PlanForward, Step: i}
		case i > s.p:
			s.plan = saga.Plan{Kind: saga.PlanManual}
		case i == 0:
			s.plan = saga.Plan{Kind: saga.PlanCompensated}
		default:
			s.plan = saga.Plan{Kind: saga.PlanCompensate, Step: i - 1}
		}
	case journal.KindCompIntent:
		s.plan = saga.Plan{Kind: saga.PlanCompensate, Step: r.Step}
	case journal.KindCompDone:
		if r.Step == 0 {
			s.plan = saga.Plan{Kind: saga.PlanCompensated}
		} else {
			s.plan = saga.Plan{Kind: saga.PlanCompensate, Step: r.Step - 1}
		}
	}
}

// actKind 枚举可执行动作。
type actKind int

const (
	aIntent actKind = iota
	aDone
	aFailT
	aFailP
	aCompIntent
	aCompDone
	aProbeT
	aProbeF
)

type action struct {
	kind actKind
	step int
}

// do 在真实管理器上执行动作。
func (a action) do(m *saga.Manager, id string) error {
	switch a.kind {
	case aIntent:
		return m.Intent(id, a.step)
	case aDone:
		return m.Done(id, a.step)
	case aFailT:
		return m.Fail(id, a.step, journal.Transient)
	case aFailP:
		return m.Fail(id, a.step, journal.Permanent)
	case aCompIntent:
		return m.CompIntent(id, a.step)
	case aCompDone:
		return m.CompDone(id, a.step)
	case aProbeT:
		return m.Probe(id, true)
	case aProbeF:
		return m.Probe(id, false)
	}
	panic("未知动作")
}

// record 返回动作将写入的日志记录（p 为枢轴下标）。
func (a action) record(p int) journal.Record {
	switch a.kind {
	case aIntent:
		return journal.Record{Kind: journal.KindIntent, Step: a.step}
	case aDone:
		return journal.Record{Kind: journal.KindDone, Step: a.step}
	case aFailT:
		return journal.Record{Kind: journal.KindFail, Step: a.step, Fail: journal.Transient}
	case aFailP:
		return journal.Record{Kind: journal.KindFail, Step: a.step, Fail: journal.Permanent}
	case aCompIntent:
		return journal.Record{Kind: journal.KindCompIntent, Step: a.step}
	case aCompDone:
		return journal.Record{Kind: journal.KindCompDone, Step: a.step}
	case aProbeT:
		return journal.Record{Kind: journal.KindDone, Step: p}
	case aProbeF:
		return journal.Record{Kind: journal.KindFail, Step: p, Fail: journal.Permanent}
	}
	panic("未知动作")
}

// legal 列出当前状态下的全部合法动作。
// inflight 为影子在途标志（-1 无在途），comp 标记在途是否为补偿。
func legalActions(pl saga.Plan, inflight int, comp bool) []action {
	if inflight >= 0 {
		if comp {
			return []action{{aCompDone, inflight}}
		}
		return []action{{aDone, inflight}, {aFailT, inflight}, {aFailP, inflight}}
	}
	switch pl.Kind {
	case saga.PlanForward:
		return []action{{aIntent, pl.Step}}
	case saga.PlanCompensate:
		return []action{{aCompIntent, pl.Step}}
	case saga.PlanProbe:
		return []action{{aProbeT, 0}, {aProbeF, 0}}
	}
	return nil
}

// apply 推进影子在途标志。
func applyShadow(a action, inflight *int, comp *bool) {
	switch a.kind {
	case aIntent:
		*inflight, *comp = a.step, false
	case aCompIntent:
		*inflight, *comp = a.step, true
	default:
		*inflight = -1
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("动作被意外拒绝: %v", err)
	}
}
