package switchrule

import "ontology/plan"

type Verdict int

const (
	Accept Verdict = iota
	Reject
	BorderlineAccept
)

type Record struct {
	D       int
	Verdict Verdict
}

type Stream struct {
	state plan.Severity
	// normal: 进入 Normal 以来的初检记录，最多保留 10 条
	hist []Record
	// tightened: 连续接收数 / 累计拒收数
	acceptRun int
	rejects   int
	// 上一次 Observe 实际扫描过的历史记录条数（证明有界窗口）
	looked int
}

type Engine struct {
	lr int
}

func New(lr int) *Engine {
	return &Engine{lr: lr}
}

func (e *Engine) NewStream() *Stream { return &Stream{state: plan.Normal} }

func (e *Engine) State(st *Stream) plan.Severity {
	return st.state
}

func (e *Engine) Observe(st *Stream, r Record) plan.Severity {
	st.looked = 0
	switch st.state {
	case plan.Normal:
		st.hist = append(st.hist, r)
		if len(st.hist) > 10 {
			st.hist = st.hist[len(st.hist)-10:]
		}
		rejectCount := 0
		allAccept10 := len(st.hist) == 10
		sumD := 0
		for idx, h := range st.hist {
			st.looked++
			sumD += h.D
			if h.Verdict != Accept {
				allAccept10 = false
			}
			if idx >= len(st.hist)-5 && h.Verdict == Reject {
				rejectCount++
			}
		}
		if rejectCount >= 2 {
			e.Enter(st, plan.Tightened)
		} else if allAccept10 && sumD <= e.lr {
			e.Enter(st, plan.Reduced)
		}
	case plan.Tightened:
		switch r.Verdict {
		case Accept:
			st.acceptRun++
		default:
			st.acceptRun = 0
			st.rejects++
		}
		if st.acceptRun >= 5 {
			e.Enter(st, plan.Normal)
		} else if st.rejects >= 5 {
			e.Enter(st, plan.Suspended)
		}
	case plan.Reduced:
		if r.Verdict != Accept {
			e.Enter(st, plan.Normal)
		}
	}
	return st.state
}

func (e *Engine) Enter(st *Stream, s plan.Severity) {
	st.state = s
	st.hist = nil
	st.acceptRun = 0
	st.rejects = 0
}

func (e *Engine) Resume(st *Stream) {
	e.Enter(st, plan.Tightened)
	st.looked = 0
}

func (e *Engine) Looked(st *Stream) int {
	return st.looked
}
