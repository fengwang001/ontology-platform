package scholarship

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"
)

// naiveEngine 是按规则独立写成的朴素参照模型:
// 不复用引擎的任何实现, 只用线性扫描与简单排序, 用于随机数据对照。
type naiveEngine struct {
	levels    []LevelConfig
	pool0     int
	students  map[string]Student
	confirmed map[string]Award
	hasResult bool
	current   []Award
}

func newNaive(cfg Config) *naiveEngine {
	return &naiveEngine{
		levels:    cfg.Levels,
		pool0:     cfg.PoolSize,
		students:  map[string]Student{},
		confirmed: map[string]Award{},
	}
}

func (n *naiveEngine) correctGrade(id string, avg float64, fail bool) error {
	if id == "" {
		return ErrInvalidParam
	}
	if avg < 0 || avg > 100 || avg != avg {
		return ErrInvalidParam
	}
	s, ok := n.students[id]
	if !ok {
		return ErrNotFound
	}
	s.AvgGrade = avg
	s.HasFailRecord = fail
	n.students[id] = s
	return nil
}

func (n *naiveEngine) registerDiscipline(id, discID string, liftedAt time.Time) error {
	if id == "" || discID == "" {
		return ErrInvalidParam
	}
	s, ok := n.students[id]
	if !ok {
		return ErrNotFound
	}
	for _, d := range s.Disciplines {
		if d.ID == discID {
			return ErrInvalidParam
		}
	}
	s.Disciplines = append(s.Disciplines, Discipline{ID: discID, LiftedAt: liftedAt})
	n.students[id] = s
	return nil
}

func (n *naiveEngine) liftDiscipline(id, discID string, liftedAt time.Time) error {
	if id == "" || discID == "" {
		return ErrInvalidParam
	}
	s, ok := n.students[id]
	if !ok {
		return ErrNotFound
	}
	for i, d := range s.Disciplines {
		if d.ID == discID {
			s.Disciplines[i].LiftedAt = liftedAt
			n.students[id] = s
			return nil
		}
	}
	return ErrNotFound
}

func (n *naiveEngine) confirm(id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	if _, ok := n.students[id]; !ok {
		return ErrNotFound
	}
	if !n.hasResult {
		return ErrNotEvaluated
	}
	var cur *Award
	for i := range n.current {
		if n.current[i].StudentID == id {
			cur = &n.current[i]
		}
	}
	if cur == nil {
		return ErrAwardNotInResult
	}
	if _, ok := n.confirmed[id]; ok {
		return ErrAlreadyConfirmed
	}
	n.confirmed[id] = *cur
	return nil
}

func naiveLess(a, b Student) bool {
	if a.AvgGrade != b.AvgGrade {
		return a.AvgGrade > b.AvgGrade
	}
	if a.Credits != b.Credits {
		return a.Credits > b.Credits
	}
	if a.HonorPoints != b.HonorPoints {
		return a.HonorPoints > b.HonorPoints
	}
	return a.ID < b.ID
}

func naiveTied(a, b Student) bool {
	return a.AvgGrade == b.AvgGrade && a.Credits == b.Credits && a.HonorPoints == b.HonorPoints
}

func naiveEligible(s Student, lv LevelConfig, at time.Time) FailReason {
	active := false
	for _, d := range s.Disciplines {
		if d.LiftedAt.IsZero() || d.LiftedAt.After(at) {
			active = true
		}
	}
	if active {
		return ReasonDiscipline
	}
	if s.HasFailRecord {
		return ReasonFailRecord
	}
	if s.Credits < lv.MinCredits {
		return ReasonCredits
	}
	if s.AvgGrade < lv.MinAvg {
		return ReasonAvgGrade
	}
	return ReasonNone
}

func (n *naiveEngine) evaluate(at time.Time) Result {
	deptSet := map[string]bool{}
	for _, lv := range n.levels {
		for d := range lv.Quotas {
			deptSet[d] = true
		}
	}
	var depts []string
	for d := range deptSet {
		depts = append(depts, d)
	}
	sort.Strings(depts)

	eligible := map[string][]bool{}
	reason := map[string][]FailReason{}
	for id, s := range n.students {
		eligible[id] = make([]bool, len(n.levels))
		reason[id] = make([]FailReason, len(n.levels))
		for li, lv := range n.levels {
			r := naiveEligible(s, lv, at)
			reason[id][li] = r
			eligible[id][li] = r == ReasonNone
		}
	}

	remaining := map[string]map[string]int{}
	for _, lv := range n.levels {
		remaining[lv.ID] = map[string]int{}
		for d, q := range lv.Quotas {
			remaining[lv.ID][d] = q
		}
	}
	pool := n.pool0
	awarded := map[string]Award{}

	var confIDs []string
	for id := range n.confirmed {
		confIDs = append(confIDs, id)
	}
	sort.Strings(confIDs)
	for _, id := range confIDs {
		a := n.confirmed[id]
		s := n.students[id]
		a.DeptID = s.DeptID
		if remaining[a.LevelID][s.DeptID] > 0 {
			remaining[a.LevelID][s.DeptID]--
			a.FromPool = false
		} else {
			pool--
			a.FromPool = true
		}
		awarded[id] = a
	}

	gather := func(dept string, li int) []Student {
		var out []Student
		for _, s := range n.students {
			if dept != "" && s.DeptID != dept {
				continue
			}
			if _, ok := awarded[s.ID]; ok {
				continue
			}
			if !eligible[s.ID][li] {
				continue
			}
			out = append(out, s)
		}
		sort.Slice(out, func(i, j int) bool { return naiveLess(out[i], out[j]) })
		return out
	}

	for li, lv := range n.levels {
		for _, d := range depts {
			cands := gather(d, li)
			for i := 0; i < len(cands); {
				j := i
				for j < len(cands) && naiveTied(cands[i], cands[j]) {
					j++
				}
				grp := cands[i:j]
				if len(grp) <= remaining[lv.ID][d] {
					for _, s := range grp {
						awarded[s.ID] = Award{StudentID: s.ID, LevelID: lv.ID, DeptID: d}
					}
					remaining[lv.ID][d] -= len(grp)
				} else {
					pool += remaining[lv.ID][d]
					remaining[lv.ID][d] = 0
					break
				}
				i = j
			}
		}
	}

	for li, lv := range n.levels {
		if pool <= 0 {
			break
		}
		cands := gather("", li)
		for i := 0; i < len(cands); {
			j := i
			for j < len(cands) && naiveTied(cands[i], cands[j]) {
				j++
			}
			grp := cands[i:j]
			if len(grp) <= pool {
				for _, s := range grp {
					awarded[s.ID] = Award{StudentID: s.ID, LevelID: lv.ID, DeptID: s.DeptID, FromPool: true}
				}
				pool -= len(grp)
			}
			i = j
		}
	}

	levelIdx := map[string]int{}
	for i, lv := range n.levels {
		levelIdx[lv.ID] = i
	}
	var awards []Award
	for _, a := range awarded {
		awards = append(awards, a)
	}
	sort.Slice(awards, func(i, j int) bool {
		if levelIdx[awards[i].LevelID] != levelIdx[awards[j].LevelID] {
			return levelIdx[awards[i].LevelID] < levelIdx[awards[j].LevelID]
		}
		if awards[i].DeptID != awards[j].DeptID {
			return awards[i].DeptID < awards[j].DeptID
		}
		return awards[i].StudentID < awards[j].StudentID
	})

	var dq []Disqualification
	var ids []string
	for id := range n.students {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for li, lv := range n.levels {
			if reason[id][li] != ReasonNone {
				dq = append(dq, Disqualification{StudentID: id, LevelID: lv.ID, Reason: reason[id][li]})
			}
		}
	}

	var rankings []DeptRanking
	for _, d := range depts {
		var ss []Student
		for _, s := range n.students {
			if s.DeptID == d {
				ss = append(ss, s)
			}
		}
		sort.Slice(ss, func(i, j int) bool { return naiveLess(ss[i], ss[j]) })
		var entries []RankEntry
		rank := 0
		for i, s := range ss {
			if i == 0 || !naiveTied(ss[i-1], s) {
				rank = i + 1
			}
			entries = append(entries, RankEntry{StudentID: s.ID, Rank: rank})
		}
		rankings = append(rankings, DeptRanking{DeptID: d, Entries: entries})
	}

	n.current = awards
	n.hasResult = true
	return Result{Awards: awards, Disqualified: dq, Rankings: rankings, PoolLeft: pool}
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidParam):
		return "invalid-param"
	case errors.Is(err, ErrNotFound):
		return "not-found"
	case errors.Is(err, ErrNotEvaluated):
		return "not-evaluated"
	case errors.Is(err, ErrAwardNotInResult):
		return "award-not-in-result"
	case errors.Is(err, ErrAlreadyConfirmed):
		return "already-confirmed"
	}
	return "unknown"
}

// resultKey 生成结果的确定性文本表示, 用于引擎与朴素模型逐项对照。
func resultKey(res Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "poolLeft=%d\n", res.PoolLeft)
	for _, a := range res.Awards {
		fmt.Fprintf(&b, "award %s %s %s pool=%v\n", a.StudentID, a.LevelID, a.DeptID, a.FromPool)
	}
	for _, d := range res.Disqualified {
		fmt.Fprintf(&b, "disqualified %s %s %s\n", d.StudentID, d.LevelID, d.Reason)
	}
	for _, dr := range res.Rankings {
		for _, en := range dr.Entries {
			fmt.Fprintf(&b, "rank %s %s %d\n", dr.DeptID, en.StudentID, en.Rank)
		}
	}
	return b.String()
}

func randomConfig(rng *rand.Rand) (Config, []string) {
	allDepts := []string{"CS", "EE", "MA"}
	depts := append([]string(nil), allDepts[:2+rng.Intn(2)]...)
	nLevels := 2 + rng.Intn(2)
	cfg := Config{PoolSize: rng.Intn(3)}
	avg := 85 + rng.Intn(11)
	credits := 16 + rng.Intn(6)
	for i := 0; i < nLevels; i++ {
		lv := LevelConfig{
			ID:         fmt.Sprintf("L%d", i+1),
			MinAvg:     float64(avg),
			MinCredits: credits,
			Quotas:     map[string]int{},
		}
		for _, d := range depts {
			lv.Quotas[d] = rng.Intn(3)
		}
		cfg.Levels = append(cfg.Levels, lv)
		avg -= 5 + rng.Intn(6)
		credits -= 3 + rng.Intn(4)
		if credits < 0 {
			credits = 0
		}
	}
	return cfg, depts
}

func randomStudent(rng *rand.Rand, id string, depts []string) Student {
	avgChoices := []float64{60, 65, 70, 75, 80, 85, 90, 95}
	creditChoices := []int{6, 10, 14, 18, 22}
	honorChoices := []int{0, 2, 5}
	return Student{
		ID:            id,
		DeptID:        depts[rng.Intn(len(depts))],
		AvgGrade:      avgChoices[rng.Intn(len(avgChoices))],
		Credits:       creditChoices[rng.Intn(len(creditChoices))],
		HonorPoints:   honorChoices[rng.Intn(len(honorChoices))],
		HasFailRecord: rng.Intn(6) == 0,
	}
}

// 与朴素模型对照随机生成的数据与操作序列, 日志打印每步输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	base := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	avgChoices := []float64{60, 65, 70, 75, 80, 85, 90, 95}

	for _, seed := range []int64{20261006, 7, 42, 999} {
		rng := rand.New(rand.NewSource(seed))
		for trial := 0; trial < 25; trial++ {
			cfg, depts := randomConfig(rng)
			eng, err := NewEngine(cfg)
			if err != nil {
				t.Fatalf("trial %d: NewEngine: %v", trial, err)
			}
			naive := newNaive(cfg)

			nStudents := 4 + rng.Intn(6)
			var ids []string
			for i := 0; i < nStudents; i++ {
				s := randomStudent(rng, fmt.Sprintf("S%d", i), depts)
				if err := eng.AddStudent(s); err != nil {
					t.Fatalf("trial %d: AddStudent: %v", trial, err)
				}
				naive.students[s.ID] = s
				ids = append(ids, s.ID)
				t.Logf("[trial %d] add student %+v", trial, s)
			}

			discs := map[string][]string{}
			pickID := func() string {
				if rng.Intn(12) == 0 {
					return "ghost"
				}
				return ids[rng.Intn(len(ids))]
			}

			nOps := 20 + rng.Intn(20)
			for step := 0; step < nOps; step++ {
				op := rng.Intn(6)
				switch op {
				case 0, 1:
					id, avg := pickID(), avgChoices[rng.Intn(len(avgChoices))]
					fail := rng.Intn(4) == 0
					errE := eng.CorrectGrade(id, avg, fail)
					errN := naive.correctGrade(id, avg, fail)
					t.Logf("[trial %d step %d] CorrectGrade(id=%s avg=%v fail=%v) -> engine=%s naive=%s",
						trial, step, id, avg, fail, errClass(errE), errClass(errN))
					if errClass(errE) != errClass(errN) {
						t.Fatalf("CorrectGrade error mismatch: engine=%v naive=%v", errE, errN)
					}
				case 2:
					id := pickID()
					discID := fmt.Sprintf("D%d", step)
					var lift time.Time
					if rng.Intn(2) == 0 {
						lift = base.Add(time.Duration(rng.Intn(5)-2) * time.Hour)
					}
					errE := eng.RegisterDiscipline(id, discID, lift)
					errN := naive.registerDiscipline(id, discID, lift)
					t.Logf("[trial %d step %d] RegisterDiscipline(id=%s disc=%s lift=%v) -> engine=%s naive=%s",
						trial, step, id, discID, lift, errClass(errE), errClass(errN))
					if errClass(errE) != errClass(errN) {
						t.Fatalf("RegisterDiscipline error mismatch: engine=%v naive=%v", errE, errN)
					}
					if errE == nil {
						discs[id] = append(discs[id], discID)
					}
				case 3:
					id := pickID()
					if len(discs[id]) == 0 {
						continue
					}
					discID := discs[id][rng.Intn(len(discs[id]))]
					lift := base.Add(time.Duration(rng.Intn(3)-1) * time.Hour)
					errE := eng.LiftDiscipline(id, discID, lift)
					errN := naive.liftDiscipline(id, discID, lift)
					t.Logf("[trial %d step %d] LiftDiscipline(id=%s disc=%s lift=%v) -> engine=%s naive=%s",
						trial, step, id, discID, lift, errClass(errE), errClass(errN))
					if errClass(errE) != errClass(errN) {
						t.Fatalf("LiftDiscipline error mismatch: engine=%v naive=%v", errE, errN)
					}
				case 4:
					resE := eng.Evaluate(base)
					resN := naive.evaluate(base)
					t.Logf("[trial %d step %d] Evaluate ->\nengine:\n%snaive:\n%s",
						trial, step, resultKey(resE), resultKey(resN))
					if resultKey(resE) != resultKey(resN) {
						t.Fatalf("trial %d step %d: result mismatch\nengine:\n%s\nnaive:\n%s",
							trial, step, resultKey(resE), resultKey(resN))
					}
					checkInvariants(t, cfg, resE)
				case 5:
					id := pickID()
					if rng.Intn(15) == 0 {
						id = ""
					}
					errE := eng.Confirm(id)
					errN := naive.confirm(id)
					t.Logf("[trial %d step %d] Confirm(id=%q) -> engine=%s naive=%s",
						trial, step, id, errClass(errE), errClass(errN))
					if errClass(errE) != errClass(errN) {
						t.Fatalf("Confirm error mismatch: engine=%v naive=%v", errE, errN)
					}
				}
			}

			// 序列结束后做最终评定对照, 并验证重评确定性。
			finalE := eng.Evaluate(base)
			finalN := naive.evaluate(base)
			if resultKey(finalE) != resultKey(finalN) {
				t.Fatalf("trial %d: final result mismatch\nengine:\n%s\nnaive:\n%s",
					trial, resultKey(finalE), resultKey(finalN))
			}
			again := eng.Evaluate(base)
			if resultKey(finalE) != resultKey(again) {
				t.Fatalf("trial %d: reevaluation not deterministic", trial)
			}
			checkInvariants(t, cfg, finalE)
			t.Logf("[trial %d] final result:\n%s", trial, resultKey(finalE))
		}
	}
}
