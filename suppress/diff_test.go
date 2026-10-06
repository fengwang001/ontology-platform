package suppress

import (
	"math/rand"
	"testing"
)

// randomInput 是一次随机试验的输入。
type randomInput struct {
	totalLines    int
	rules         []string
	requireReason bool
	diagnostics   []Diagnostic
	directives    []Directive
}

func genRandom(rng *rand.Rand) randomInput {
	total := 1 + rng.Intn(8)
	rules := []string{"A", "B", "C"}
	in := randomInput{totalLines: total, rules: rules, requireReason: rng.Intn(2) == 0}

	rulePool := append(append([]string{}, rules...), AllTag, "X", "Y")
	nd := rng.Intn(10)
	for i := 0; i < nd; i++ {
		d := Diagnostic{
			Line:   1 + rng.Intn(total),
			Column: 1 + rng.Intn(5),
			Rule:   rules[rng.Intn(len(rules))],
		}
		in.diagnostics = append(in.diagnostics, d)
	}
	nk := rng.Intn(12)
	for i := 0; i < nk; i++ {
		d := Directive{Line: 1 + rng.Intn(total), Kind: Kind(rng.Intn(5))}
		nl := rng.Intn(4)
		for j := 0; j < nl; j++ {
			d.Labels = append(d.Labels, rulePool[rng.Intn(len(rulePool))])
		}
		switch rng.Intn(3) {
		case 0:
			d.Reason = ""
		case 1:
			d.Reason = "   "
		default:
			d.Reason = "reason"
		}
		in.directives = append(in.directives, d)
	}
	return in
}

// toSnapshot 将随机输入经正常登记通道转为快照；
// 丢弃被拒绝登记（生成器不会产出非法行，但可能产出完全重复指令），
// 这同时验证了“被拒绝登记不改变状态”。
func (in randomInput) toSnapshot(t *testing.T) (snapshot, []RegError) {
	t.Helper()
	s := NewSession(in.totalLines, in.rules, in.requireReason)
	var rejected []RegError
	for _, d := range in.diagnostics {
		if err := s.AddDiagnostic(d); err != nil {
			rejected = append(rejected, *err)
		}
	}
	for _, d := range in.directives {
		if err := s.AddDirective(d); err != nil {
			rejected = append(rejected, *err)
		}
	}
	return s.takeSnapshot(), rejected
}

// TestRandomDifferential 以大量随机输入将实现与独立朴素模型逐字段对照。
func TestRandomDifferential(t *testing.T) {
	const iterations = 4000
	rng := rand.New(rand.NewSource(20261006))
	for it := 0; it < iterations; it++ {
		in := genRandom(rng)
		snap, rejected := in.toSnapshot(t)
		got := evaluate(snap)
		want := naiveEvaluate(snap)

		if !judgmentsEqual(got, want) {
			name := "随机对照#" + itoa(it)
			logJudgment(t, name+"(实现)", in.totalLines, in.requireReason,
				in.diagnostics, in.directives, got)
			logJudgment(t, name+"(朴素)", in.totalLines, in.requireReason,
				in.diagnostics, in.directives,
				Judgment{Kept: want.kept, Suppressed: want.suppressed, Issues: want.issues})
			t.Fatalf("第 %d 次随机输入与朴素模型不一致", it)
		}

		// 前若干次与发生重复拒绝的样例打印完整日志（输入/输出/判定依据）。
		if it < 10 || len(rejected) > 0 {
			logJudgment(t, "随机对照#"+itoa(it), in.totalLines, in.requireReason,
				in.diagnostics, in.directives, got)
		}
	}
}

func judgmentsEqual(got Judgment, want naiveResult) bool {
	if len(got.Kept) != len(want.kept) || len(got.Suppressed) != len(want.suppressed) ||
		len(got.Issues) != len(want.issues) {
		return false
	}
	for i := range got.Kept {
		if got.Kept[i] != want.kept[i] {
			return false
		}
	}
	for i := range got.Suppressed {
		a, b := got.Suppressed[i], want.suppressed[i]
		if a.Diagnostic != b.Diagnostic || a.Attribution != b.Attribution {
			return false
		}
	}
	for i := range got.Issues {
		if got.Issues[i] != want.issues[i] {
			return false
		}
	}
	return true
}
