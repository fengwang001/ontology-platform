package audit_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"ontology/audit"
)

// naiveSystem 是独立维护的朴素参照实现：线性扫描、直白存储，
// 与被测系统不共享任何存储/查找逻辑，仅共享纯函数裁决语义 audit.Evaluate。
type naiveSystem struct {
	versions   map[audit.VersionID]audit.RuleSet
	verCurrent map[audit.VersionID]audit.RuleSet // 当前实际存储内容（含篡改）
	verOrder   []audit.VersionID
	verGone    map[audit.VersionID]bool
	verTamper  map[audit.VersionID]bool

	records   map[audit.RecordID]*naiveRecord
	recOrder  []audit.RecordID
	recTamper map[audit.RecordID]bool

	corr      map[audit.CorrectionID]*audit.Correction
	corrOrder []audit.CorrectionID
	byRecord  map[audit.RecordID][]audit.CorrectionID
}

type naiveRecord struct {
	subject     string
	target      string
	req         audit.Request
	decidedAt   time.Time
	result      audit.Decision
	versionID   audit.VersionID
	versionHash string
}

func newNaive() *naiveSystem {
	return &naiveSystem{
		versions:   make(map[audit.VersionID]audit.RuleSet),
		verCurrent: make(map[audit.VersionID]audit.RuleSet),
		verGone:    make(map[audit.VersionID]bool),
		verTamper:  make(map[audit.VersionID]bool),
		records:    make(map[audit.RecordID]*naiveRecord),
		recTamper:  make(map[audit.RecordID]bool),
		corr:       make(map[audit.CorrectionID]*audit.Correction),
		byRecord:   make(map[audit.RecordID][]audit.CorrectionID),
	}
}

func (n *naiveSystem) submit(content audit.RuleSet) audit.VersionID {
	id := audit.VersionID(fmt.Sprintf("rv-%d", len(n.verOrder)+1))
	n.versions[id] = content
	n.verCurrent[id] = content
	n.verOrder = append(n.verOrder, id)
	return id
}

func (n *naiveSystem) recordAccess(subject, target string, req audit.Request, at time.Time, v audit.VersionID, result audit.Decision) (audit.RecordID, error) {
	content, ok := n.versions[v]
	if !ok || n.verGone[v] {
		return "", audit.ErrVersionNotFound
	}
	id := audit.RecordID(fmt.Sprintf("ar-%d", len(n.recOrder)+1))
	n.records[id] = &naiveRecord{
		subject: subject, target: target, req: req, decidedAt: at,
		result: result, versionID: v, versionHash: audit.ContentHash(content),
	}
	n.recOrder = append(n.recOrder, id)
	return id, nil
}

func (n *naiveSystem) replay(recID audit.RecordID) (audit.ReplayResult, audit.ReadStats, error) {
	var stats audit.ReadStats
	rec, ok := n.records[recID]
	if !ok {
		return audit.ReplayResult{}, stats, audit.ErrRecordNotFound
	}
	stats.RecordsScanned = 1
	_, ok = n.versions[rec.versionID]
	if !ok || n.verGone[rec.versionID] {
		return audit.ReplayResult{}, stats, audit.ErrVersionNotFound
	}
	stats.VersionsScanned = 1
	stats.VersionBytesRead = len(audit.Canonical(n.verCurrent[rec.versionID]))
	if n.recTamper[recID] {
		return audit.ReplayResult{}, stats, audit.ErrAuditIntegrity
	}
	res := audit.ReplayResult{
		RecordID: recID, Original: rec.result,
		VersionID: rec.versionID, VersionHash: rec.versionHash,
	}
	if n.verTamper[rec.versionID] {
		res.Outcome = audit.ReplayVersionTampered
		res.Recomputed = rec.result
		return res, stats, nil
	}
	res.Recomputed = audit.Evaluate(n.versions[rec.versionID], rec.subject, rec.target, rec.req)
	if res.Recomputed == rec.result {
		res.Outcome = audit.ReplayConsistent
	} else {
		res.Outcome = audit.ReplayMisjudgment
	}
	return res, stats, nil
}

func (n *naiveSystem) appendCorrection(recID audit.RecordID, supersedes audit.CorrectionID, to audit.Decision, reason string) (audit.CorrectionID, error) {
	if _, ok := n.records[recID]; !ok {
		return "", audit.ErrCorrectionTargetMissing
	}
	chain := n.byRecord[recID]
	if supersedes == "" {
		if len(chain) != 0 {
			return "", audit.ErrCorrectionChain
		}
	} else {
		sc, ok := n.corr[supersedes]
		if !ok {
			return "", audit.ErrCorrectionNotFound
		}
		if sc.RecordID != recID || len(chain) == 0 || chain[len(chain)-1] != supersedes {
			return "", audit.ErrCorrectionChain
		}
	}
	id := audit.CorrectionID(fmt.Sprintf("cr-%d", len(n.corrOrder)+1))
	c := &audit.Correction{ID: id, RecordID: recID, Supersedes: supersedes, CorrectedTo: to, Reason: reason}
	n.corr[id] = c
	n.corrOrder = append(n.corrOrder, id)
	n.byRecord[recID] = append(n.byRecord[recID], id)
	return id, nil
}

func (n *naiveSystem) effective(recID audit.RecordID) (audit.Legality, audit.Decision, error) {
	rec, ok := n.records[recID]
	if !ok {
		return audit.LegalityDenyAsRecorded, audit.DecisionDeny, audit.ErrRecordNotFound
	}
	eff := rec.result
	if chain := n.byRecord[recID]; len(chain) > 0 {
		eff = n.corr[chain[len(chain)-1]].CorrectedTo
	}
	if eff != rec.result {
		return audit.LegalityCorrected, eff, nil
	}
	if rec.result == audit.DecisionAllow {
		return audit.LegalityAllowAsRecorded, eff, nil
	}
	return audit.LegalityDenyAsRecorded, eff, nil
}

func (n *naiveSystem) queryLegality(subject, target string, at time.Time) (audit.Legality, error) {
	var found audit.RecordID
	for _, id := range n.recOrder {
		r := n.records[id]
		if r.subject == subject && r.target == target && r.decidedAt.Equal(at) {
			found = id
		}
	}
	if found == "" {
		return audit.LegalityDenyAsRecorded, audit.ErrRecordNotFound
	}
	leg, _, err := n.effective(found)
	return leg, err
}

// ---- 随机差分对照 ----

var (
	diffSubjects = []string{"alice", "bob", "carol", "dave"}
	diffTargets  = []string{"doc-1", "doc-2", "doc-3"}
	diffActions  = []string{"read", "write", "delete"}
)

func randomRuleSet(r *rand.Rand) audit.RuleSet {
	n := 1 + r.Intn(3)
	rules := make([]audit.Rule, 0, n)
	for i := 0; i < n; i++ {
		effect := audit.EffectDeny
		if r.Intn(2) == 0 {
			effect = audit.EffectAllow
		}
		pick := func(pool []string) []string {
			if r.Intn(4) == 0 {
				return []string{"*"}
			}
			return []string{pool[r.Intn(len(pool))]}
		}
		rules = append(rules, audit.Rule{
			Effect:   effect,
			Subjects: pick(diffSubjects),
			Targets:  pick(diffTargets),
			Actions:  pick(diffActions),
		})
	}
	return audit.RuleSet{Rules: rules}
}

func sameErr(got, want error) bool {
	if got == nil || want == nil {
		return got == want
	}
	return errors.Is(got, want)
}

// TestDifferential 在大量随机规则版本与访问序列上，
// 将被测系统与朴素参照实现逐项对照，包括篡改注入后的检测行为。
func TestDifferential(t *testing.T) {
	seeds := []int64{1, 7, 42, 2026, 99173}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			runDifferential(t, rand.New(rand.NewSource(seed)), 400)
		})
	}
}

func runDifferential(t *testing.T, r *rand.Rand, steps int) {
	t.Helper()
	sys := audit.New(audit.WithClock(func() time.Time { return t0 }))
	ref := newNaive()

	var vers []audit.VersionID
	var recs []audit.RecordID
	var times []time.Time

	pickVer := func() audit.VersionID {
		if len(vers) == 0 || r.Intn(20) == 0 {
			return audit.VersionID(fmt.Sprintf("rv-%d", r.Intn(len(vers)+3)+1))
		}
		return vers[r.Intn(len(vers))]
	}
	pickRec := func() audit.RecordID {
		if len(recs) == 0 || r.Intn(20) == 0 {
			return audit.RecordID(fmt.Sprintf("ar-%d", r.Intn(len(recs)+3)+1))
		}
		return recs[r.Intn(len(recs))]
	}

	for step := 0; step < steps; step++ {
		op := r.Intn(100)
		switch {
		case op < 20: // 提交规则版本
			content := randomRuleSet(r)
			gotID, gotErr := sys.SubmitRuleVersion(content)
			wantID := ref.submit(content)
			if gotErr != nil || gotID != wantID {
				t.Fatalf("step %d submit: got %v/%v want %v", step, gotID, gotErr, wantID)
			}
			vers = append(vers, gotID)

		case op < 45: // 记录访问判定（部分注入误判）
			subject := diffSubjects[r.Intn(len(diffSubjects))]
			target := diffTargets[r.Intn(len(diffTargets))]
			req := audit.Request{Action: diffActions[r.Intn(len(diffActions))]}
			at := t0.Add(time.Duration(r.Intn(50)) * time.Minute)
			v := pickVer()
			result := audit.Decision(r.Intn(2) == 0) // 随机结果，天然覆盖正确与误判
			gotID, gotErr := sys.RecordAccess(subject, target, req, at, v, result)
			wantID, wantErr := ref.recordAccess(subject, target, req, at, v, result)
			if !sameErr(gotErr, wantErr) || gotID != wantID {
				t.Fatalf("step %d record: got %v/%v want %v/%v", step, gotID, gotErr, wantID, wantErr)
			}
			if gotErr == nil {
				recs = append(recs, gotID)
				times = append(times, at)
			}

		case op < 60: // 回放
			rec := pickRec()
			gotRes, gotStats, gotErr := sys.Replay(rec)
			wantRes, wantStats, wantErr := ref.replay(rec)
			if !sameErr(gotErr, wantErr) {
				t.Fatalf("step %d replay %s: err got %v want %v", step, rec, gotErr, wantErr)
			}
			if gotErr == nil && gotRes != wantRes {
				t.Fatalf("step %d replay %s: got %+v want %+v", step, rec, gotRes, wantRes)
			}
			if gotStats != wantStats {
				t.Fatalf("step %d replay %s: stats got %+v want %+v", step, rec, gotStats, wantStats)
			}

		case op < 75: // 追加纠正
			rec := pickRec()
			var supersedes audit.CorrectionID
			if r.Intn(2) == 0 {
				if chain, err := sys.CorrectionsOf(rec); err == nil && len(chain) > 0 {
					supersedes = chain[len(chain)-1].ID
				}
			} else if r.Intn(10) == 0 {
				supersedes = audit.CorrectionID(fmt.Sprintf("cr-%d", r.Intn(5)+1))
			}
			to := audit.Decision(r.Intn(2) == 0)
			gotID, gotErr := sys.AppendCorrection(rec, supersedes, to, "diff")
			wantID, wantErr := ref.appendCorrection(rec, supersedes, to, "diff")
			if !sameErr(gotErr, wantErr) || (gotErr == nil && gotID != wantID) {
				t.Fatalf("step %d correct %s: got %v/%v want %v/%v", step, rec, gotID, gotErr, wantID, wantErr)
			}

		case op < 85: // 三态查询
			subject := diffSubjects[r.Intn(len(diffSubjects))]
			target := diffTargets[r.Intn(len(diffTargets))]
			var at time.Time
			if len(times) > 0 && r.Intn(2) == 0 {
				at = times[r.Intn(len(times))]
			} else {
				at = t0.Add(time.Duration(r.Intn(50)) * time.Minute)
			}
			gotLeg, _, _, gotErr := sys.QueryLegality(subject, target, at)
			wantLeg, wantErr := ref.queryLegality(subject, target, at)
			if !sameErr(gotErr, wantErr) || (gotErr == nil && gotLeg != wantLeg) {
				t.Fatalf("step %d query %s/%s: got %v/%v want %v/%v", step, subject, target, gotLeg, gotErr, wantLeg, wantErr)
			}

		case op < 90: // 有效结论
			rec := pickRec()
			gotLeg, gotEff, gotErr := sys.EffectiveDecision(rec)
			wantLeg, wantEff, wantErr := ref.effective(rec)
			if !sameErr(gotErr, wantErr) || (gotErr == nil && (gotLeg != wantLeg || gotEff != wantEff)) {
				t.Fatalf("step %d effective %s: got %v/%v/%v want %v/%v/%v",
					step, rec, gotLeg, gotEff, gotErr, wantLeg, wantEff, wantErr)
			}

		default: // 注入存储层篡改（双方同步镜像）
			if len(vers) == 0 || len(recs) == 0 {
				continue
			}
			switch r.Intn(5) {
			case 0: // 篡改版本内容
				v := vers[r.Intn(len(vers))]
				err := sys.UnsafeCorruptVersionContent(v, func(rs *audit.RuleSet) {
					rs.Rules = append(rs.Rules, audit.Rule{Effect: audit.EffectAllow, Subjects: []string{"*"}, Targets: []string{"*"}, Actions: []string{"*"}})
				})
				if err == nil {
					ref.verTamper[v] = true
					mutated := ref.verCurrent[v]
					mutated.Rules = append(append([]audit.Rule(nil), mutated.Rules...),
						audit.Rule{Effect: audit.EffectAllow, Subjects: []string{"*"}, Targets: []string{"*"}, Actions: []string{"*"}})
					ref.verCurrent[v] = mutated
				}
			case 1: // 篡改记录结果
				rec := recs[r.Intn(len(recs))]
				gr, _ := sys.GetRecord(rec)
				if err := sys.UnsafeCorruptRecordResult(rec, !gr.Result); err == nil {
					ref.recTamper[rec] = true
					gr, _ := sys.GetRecord(rec)
					ref.records[rec].result = gr.Result
				}
			case 2: // 篡改记录登记的版本标识
				rec := recs[r.Intn(len(recs))]
				v := pickVer()
				if err := sys.UnsafeCorruptRecordVersionRef(rec, v); err == nil {
					if v != ref.records[rec].versionID {
						ref.recTamper[rec] = true
					}
					ref.records[rec].versionID = v
				}
			case 3: // 篡改记录链哈希
				rec := recs[r.Intn(len(recs))]
				if err := sys.UnsafeCorruptRecordHash(rec, "ffff"); err == nil {
					ref.recTamper[rec] = true
				}
			case 4: // 非法删除版本
				v := vers[r.Intn(len(vers))]
				sys.UnsafeDeleteVersion(v)
				ref.verGone[v] = true
			}
		}
	}

	// 终态全量对照：所有版本、记录、纠正链逐项一致。
	for i, v := range vers {
		gv, err := sys.GetVersion(v)
		if ref.verGone[v] {
			if !errors.Is(err, audit.ErrVersionNotFound) {
				t.Fatalf("final: deleted version %s still readable", v)
			}
			continue
		}
		if err != nil {
			t.Fatalf("final: version %s: %v", v, err)
		}
		if gv.Seq != uint64(i+1) {
			t.Fatalf("final: version %s seq %d, want %d", v, gv.Seq, i+1)
		}
		if !ref.verTamper[v] && !reflect.DeepEqual(gv.Content, ref.versions[v]) {
			t.Fatalf("final: version %s content drifted", v)
		}
	}
	for i, rec := range recs {
		gr, err := sys.GetRecord(rec)
		if err != nil {
			t.Fatalf("final: record %s: %v", rec, err)
		}
		if gr.Seq != uint64(i+1) {
			t.Fatalf("final: record %s seq %d, want %d", rec, gr.Seq, i+1)
		}
		nr := ref.records[rec]
		if gr.Subject != nr.subject || gr.Target != nr.target || gr.Result != nr.result || gr.VersionID != nr.versionID {
			t.Fatalf("final: record %s fields drifted: %+v vs %+v", rec, gr, nr)
		}
		chain, err := sys.CorrectionsOf(rec)
		if err != nil {
			t.Fatalf("final: corrections of %s: %v", rec, err)
		}
		wantChain := ref.byRecord[rec]
		if len(chain) != len(wantChain) {
			t.Fatalf("final: record %s correction chain len %d want %d", rec, len(chain), len(wantChain))
		}
		for j, c := range chain {
			if c.ID != wantChain[j] || c.CorrectedTo != ref.corr[wantChain[j]].CorrectedTo {
				t.Fatalf("final: correction %d of %s drifted", j, rec)
			}
		}
	}
}
