package judge

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"ontology/bitemporal"
	"ontology/bitemporal/registry"
)

// naiveReference 是与产品代码完全独立实现的“按规则逐步判定”的朴素参照模型。
// 它刻意不调用 interval / boundary / judge 任何代码，只用最直白的规则重写一遍：
//  1. 先逐条轴查区间是否起点晚于终点；
//  2. 查源/目标版本是否可识别；
//  3. 对每条共有轴，枚举所有候选查询时点（区间端点及其邻域），
//     逐点用最原始的不等式判断两种闭合约定下归属是否不同；
//  4. 再按轴记录与否收集损失/填充。
type naiveReference struct {
	formats map[string]registry.Format
}

func newNaiveReference(reg *registry.Registry) *naiveReference {
	// 通过注册测试中可能用到的全部版本重建一份独立映射。
	nr := &naiveReference{formats: map[string]registry.Format{}}
	for _, v := range []string{"V1", "V2", "V3", "V4", "V2O", "V2R", "V3V", "V5"} {
		if f, ok := reg.Lookup(v); ok {
			nr.formats[v] = f
		}
	}
	return nr
}

type naiveVerdict struct {
	Verdict      string
	RejectAxes   []string
	BoundaryAxes []string
	LossAxes     []string
	FillAxes     []string
	UnknownSrc   bool
	UnknownDst   bool
}

func naiveContains(iv *bitemporal.Interval, conv bitemporal.BoundaryConvention, t int64) bool {
	if iv == nil {
		return false
	}
	inside := true
	if iv.Start != nil {
		if conv.StartClosed == bitemporal.Closed {
			inside = inside && t >= *iv.Start
		} else {
			inside = inside && t > *iv.Start
		}
	}
	if iv.End != nil {
		if conv.EndClosed == bitemporal.Closed {
			inside = inside && t <= *iv.End
		} else {
			inside = inside && t < *iv.End
		}
	}
	return inside
}

func (nr *naiveReference) judge(rec bitemporal.Record, src, dst string) naiveVerdict {
	out := naiveVerdict{
		RejectAxes: []string{}, BoundaryAxes: []string{},
		LossAxes: []string{}, FillAxes: []string{},
	}

	axisIV := map[bitemporal.Axis]*bitemporal.Interval{
		bitemporal.ValidTime:       rec.Valid,
		bitemporal.TransactionTime: rec.Transaction,
	}

	// 步骤 1：自洽性先行。
	for _, axis := range bitemporal.Axes() {
		iv := axisIV[axis]
		if iv != nil && iv.Start != nil && iv.End != nil && *iv.Start > *iv.End {
			out.RejectAxes = append(out.RejectAxes, string(axis))
		}
	}
	if len(out.RejectAxes) > 0 {
		out.Verdict = string(VerdictRejected)
		return out
	}

	// 步骤 2：版本可识别性。
	srcFmt, srcOK := nr.formats[src]
	dstFmt, dstOK := nr.formats[dst]
	out.UnknownSrc = !srcOK
	out.UnknownDst = !dstOK
	if !srcOK || !dstOK {
		out.Verdict = string(VerdictUnknownVersion)
		return out
	}

	// 步骤 3：边界语义——朴素地枚举候选查询时点，逐点比较归属。
	for _, axis := range bitemporal.Axes() {
		iv := axisIV[axis]
		if iv == nil || !srcFmt.RecordsAxis(axis) || !dstFmt.RecordsAxis(axis) {
			continue
		}
		candidates := map[int64]struct{}{}
		for _, end := range []*int64{iv.Start, iv.End} {
			if end != nil {
				for _, d := range []int64{-1, 0, 1} {
					candidates[*end+d] = struct{}{}
				}
			}
		}
		points := make([]int64, 0, len(candidates))
		for t := range candidates {
			points = append(points, t)
		}
		sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
		for _, t := range points {
			if naiveContains(iv, srcFmt.Convention(axis), t) !=
				naiveContains(iv, dstFmt.Convention(axis), t) {
				out.BoundaryAxes = append(out.BoundaryAxes, string(axis))
				break
			}
		}
	}
	if len(out.BoundaryAxes) > 0 {
		out.Verdict = string(VerdictIncompatible)
		return out
	}

	// 步骤 4：轴留存/填充。
	for _, axis := range bitemporal.Axes() {
		srcHas := srcFmt.RecordsAxis(axis)
		dstHas := dstFmt.RecordsAxis(axis)
		if srcHas && !dstHas && axisIV[axis] != nil {
			out.LossAxes = append(out.LossAxes, string(axis))
		}
		if !srcHas && dstHas && axis == bitemporal.ValidTime {
			out.FillAxes = append(out.FillAxes, string(axis))
		}
	}
	if len(out.LossAxes) > 0 {
		out.Verdict = string(VerdictLossy)
	} else {
		out.Verdict = string(VerdictCompatible)
	}
	return out
}

// logEntry 记录一次判定的输入、输出与依据，供复核与审计。
type logEntry struct {
	Case      int            `json:"case"`
	Source    string         `json:"source_version"`
	Target    string         `json:"target_version"`
	Record    map[string]any `json:"record"`
	Engine    map[string]any `json:"engine_result"`
	Reference naiveVerdict   `json:"naive_reference"`
	Basis     []string       `json:"basis"`
}

func intervalLog(iv *bitemporal.Interval) map[string]any {
	if iv == nil {
		return nil
	}
	return map[string]any{
		"start": iv.Start, "end": iv.End,
		"start_closed": iv.StartClosed == bitemporal.Closed,
		"end_closed":   iv.EndClosed == bitemporal.Closed,
	}
}

func resultSummary(r Result) map[string]any {
	boundary := []string{}
	for _, b := range r.BoundaryIssues {
		boundary = append(boundary, string(b.Axis))
	}
	losses, fills := []string{}, []string{}
	rejects := []string{}
	for _, e := range r.RejectErrors {
		rejects = append(rejects, string(e.Axis))
	}
	for _, l := range r.Losses {
		losses = append(losses, string(l.Axis))
	}
	for _, f := range r.Fills {
		fills = append(fills, string(f.Axis))
	}
	return map[string]any{
		"verdict":       string(r.Verdict),
		"reject_axes":   rejects,
		"boundary_axes": boundary,
		"loss_axes":     losses,
		"fill_axes":     fills,
	}
}

func TestRandomVersusNaiveReference(t *testing.T) {
	eng := newTestEngine()
	nr := newNaiveReference(registry.New())

	const cases = 4000
	rng := rand.New(rand.NewSource(20261007))
	versions := []string{"V1", "V2", "V3", "V4", "V2O", "V2R", "V3V", "V5", "VUNK"}

	logPath := filepath.Join(t.TempDir(), "bitemporal_judgments.jsonl")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	enc := json.NewEncoder(logFile)

	for n := 0; n < cases; n++ {
		rec := randomRecord(rng)
		src := versions[rng.Intn(len(versions))]
		dst := versions[rng.Intn(len(versions))]

		got := eng.Judge(rec, src, dst)
		want := nr.judge(rec, src, dst)

		gotSummary := resultSummary(got)
		basis := buildBasis(got)
		entry := logEntry{
			Case: n, Source: src, Target: dst,
			Record: map[string]any{
				"id":          rec.ID,
				"valid":       intervalLog(rec.Valid),
				"transaction": intervalLog(rec.Transaction),
			},
			Engine: gotSummary, Reference: want, Basis: basis,
		}
		if err := enc.Encode(entry); err != nil {
			t.Fatal(err)
		}

		if gotSummary["verdict"] != want.Verdict {
			t.Fatalf("case %d: verdict engine=%v want=%s (src=%s dst=%s rec=%+v)",
				n, gotSummary["verdict"], want.Verdict, src, dst, rec)
		}
		if !equalStringSet(gotSummary["reject_axes"].([]string), want.RejectAxes) ||
			!equalStringSet(gotSummary["boundary_axes"].([]string), want.BoundaryAxes) ||
			!equalStringSet(gotSummary["loss_axes"].([]string), want.LossAxes) ||
			!equalStringSet(gotSummary["fill_axes"].([]string), want.FillAxes) {
			t.Fatalf("case %d: 分类轴集合不一致 engine=%+v want=%+v",
				n, gotSummary, want)
		}
	}

	// 日志可复核：重新读回，行数必须等于用例数，且每行都是合法 JSON。
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var e logEntry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("日志行不可解析: %v", err)
		}
		lines++
	}
	if lines != cases {
		t.Fatalf("日志记录数 = %d，期望 %d（路径 %s）", lines, cases, logPath)
	}
	t.Logf("已写入 %d 条判定输入/输出/依据日志：%s", cases, logPath)
}

func randomRecord(rng *rand.Rand) bitemporal.Record {
	rec := bitemporal.Record{ID: "rand"}
	mk := func() *bitemporal.Interval {
		// 1/4 概率缺轴；否则在小整数域内生成，便于朴素模型枚举邻域点。
		if rng.Intn(4) == 0 {
			return nil
		}
		a := int64(rng.Intn(7))
		b := int64(rng.Intn(7))
		iv := &bitemporal.Interval{Start: &a, End: &b}
		// 1/5 概率制造无界端，覆盖无界路径。
		if rng.Intn(5) == 0 {
			iv.Start = nil
		}
		if rng.Intn(5) == 0 {
			iv.End = nil
		}
		if rng.Intn(2) == 0 {
			iv.StartClosed = bitemporal.Closed
		}
		if rng.Intn(2) == 0 {
			iv.EndClosed = bitemporal.Closed
		}
		return iv
	}
	rec.Valid = mk()
	rec.Transaction = mk()
	return rec
}

func buildBasis(r Result) []string {
	basis := []string{}
	switch r.Verdict {
	case VerdictRejected:
		basis = append(basis, "优先级1: 记录自身时态区间起点晚于终点，先行拒绝")
	case VerdictUnknownVersion:
		basis = append(basis, "优先级4: 版本号超出可识别范围 "+r.SourceVersion+"/"+r.TargetVersion)
	case VerdictIncompatible:
		for _, b := range r.BoundaryIssues {
			basis = append(basis, b.Message)
		}
	case VerdictLossy:
		for _, l := range r.Losses {
			basis = append(basis, l.Message)
		}
		for _, f := range r.Fills {
			basis = append(basis, f.Message)
		}
	case VerdictCompatible:
		basis = append(basis, "两版本共有的轴边界归属一致，且不存在轴丢失；缺轴按固定规则填充（如有）")
		for _, f := range r.Fills {
			basis = append(basis, f.Message)
		}
	}
	return basis
}

func equalStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}
