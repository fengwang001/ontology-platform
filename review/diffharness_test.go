package review

import (
	"fmt"
	"sort"
	"time"
)

func runProd(s *Service, op Op) (string, string) {
	// 朴素模型时间为自增整数，这里同步映射为 testEpoch + t 分钟。
	return prodClock.run(s, op)
}

type prodClockRunner struct {
	t int64
}

var prodClock = &prodClockRunner{}

func (p *prodClockRunner) run(s *Service, op Op) (string, string) {
	p.t++
	at := testEpoch.Add(minutes(p.t))
	errName := func(err error) string {
		if err == nil {
			return ""
		}
		if e, ok := err.(*Error); ok {
			return codeName(e.Code)
		}
		return err.Error()
	}
	finish := func(res string, err error) (string, string) {
		if err != nil {
			return "", errName(err)
		}
		return res, ""
	}
	_ = finish
	switch op.name {
	case "addReviewer":
		id, u, g := op.args[0], op.args[1], op.args[2]
		err := s.AddReviewer(Reviewer{ID: id, Unit: "U" + itoa(u), Group: grpName(g)})
		return finish("ok", err)
	case "addApplicant":
		id, u := op.args[0], op.args[1]
		err := s.AddApplicant(Applicant{ID: id, Unit: "U" + itoa(u)})
		return finish("ok", err)
	case "relation":
		err := s.RegisterRelation(at, op.args[0], op.args[1])
		return finish("ok", err)
	case "recusalRequest":
		err := s.AcceptRecusalRequest(at, op.args[0], op.args[1])
		return finish("ok", err)
	case "create":
		minG := map[string]int{}
		if op.args[2] == 1 {
			minG["B"] = ceilDiv(op.args[1], 2)
		}
		id, panel, err := s.CreateReview(at, op.args[0], op.args[1], minG)
		if err != nil {
			return "", errName(err)
		}
		return fmt.Sprintf("review=%d panel=%v", id, panel), ""
	case "vote":
		err := s.Vote(at, op.args[0], op.args[1], op.args[2], Choice(op.args[3]))
		return finish("ok", err)
	case "recuse":
		sub, err := s.HandleRecusal(at, op.args[0], op.args[1])
		if err != nil {
			return "", errName(err)
		}
		return fmt.Sprintf("sub=%d", sub), ""
	case "objection":
		err := s.AcceptObjection(at, op.args[0])
		return finish("ok", err)
	case "adjudge":
		err := s.AdjudicateObjection(at, op.args[0], op.args[1] == 1)
		if err != nil {
			return "", errName(err)
		}
		if op.args[1] == 1 {
			return "void", ""
		}
		return "ok", ""
	case "finalize":
		st, err := s.Finalize(at, op.args[0])
		if err != nil {
			return "", errName(err)
		}
		if st == StatusFinalPass {
			return "pass", ""
		}
		return "fail", ""
	}
	return "", "未知操作"
}

func minutes(n int64) time.Duration { return time.Duration(n) * time.Minute }

// stateSummary 提取真实 Service 中与朴素模型可比的评审状态摘要。
func stateSummary(s *Service) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]int, 0, len(s.reviews))
	for id := range s.reviews {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := ""
	for _, id := range ids {
		r := s.reviews[id]
		panel := sortedPanel(r.panel)
		name := map[ReviewStatus]string{
			StatusVoting: "voting", StatusPublicity: "publicity",
			StatusFinalPass: "pass", StatusFinalFail: "fail",
			StatusVoid: "void", StatusAborted: "aborted",
		}[r.status]
		out += fmt.Sprintf("r%d{%s app=%d round=%d ep=%d panel=%v ver=%d obj=%v adj=%v};\n",
			id, name, r.applicant, r.currentRound, r.round2Episode, panel, r.version,
			r.objection, r.adjudicated)
	}
	return out
}

func naiveSummary(m *naiveModel) string {
	ids := make([]int, 0, len(m.reviews))
	for id := range m.reviews {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := ""
	for _, id := range ids {
		r := m.reviews[id]
		panel := make([]int, 0, len(r.panel))
		for p := range r.panel {
			panel = append(panel, p)
		}
		sort.Ints(panel)
		out += fmt.Sprintf("r%d{%s app=%d round=%d ep=%d panel=%v ver=%d obj=%v adj=%v};\n",
			id, r.status, r.app, r.round, r.episode, panel, r.version, r.objection, r.adjudged)
	}
	return out
}
