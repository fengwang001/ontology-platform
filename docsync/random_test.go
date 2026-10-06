package docsync

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// 随机差分：用相同操作序列驱动 Service（treap）与 naiveModel（整篇重建 +
// 线性偏移推演），逐操作比对接受/拒绝、文本、版本、有效诊断偏移、失效清单、
// 位置/偏移换算。每次输入、输出与判定依据写入日志。

var randAlphabet = []rune("ab\n\n😀\rxy")

func randRuneText(r *rand.Rand, maxLen int) string {
	n := r.Intn(maxLen + 1)
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteRune(randAlphabet[r.Intn(len(randAlphabet))])
	}
	return sb.String()
}

type randModel struct {
	seed  int
	svc   *Service
	naive *naiveModel
	log   *strings.Builder
	logf  *os.File
}

func runRandomSequence(t *testing.T, seed, ops int, logf *os.File) {
	t.Helper()
	r := rand.New(rand.NewSource(int64(seed)))
	initial := randRuneText(r, 12)
	m := &randModel{
		seed:  seed,
		svc:   NewService(initial),
		naive: newNaive(initial),
		log:   &strings.Builder{},
		logf:  logf,
	}
	fmt.Fprintf(m.log, "=== seed=%d ops=%d initial=%q ===\n", seed, ops, initial)

	for step := 0; step < ops; step++ {
		k := r.Intn(100)
		switch {
		case k < 62:
			m.doChange(t, r, step)
		case k < 88:
			m.doRegister(t, r, step)
		default:
			m.doConversions(t, r, step)
		}
		m.assertEqual(t, step)
	}
	if logf != nil {
		logf.WriteString(m.log.String())
		logf.Sync()
	}
}

// doChange 生成一组同时坐标（互不重叠）的编辑，随机制造各类拒绝场景。
func (m *randModel) doChange(t *testing.T, r *rand.Rand, step int) {
	ver := m.svc.Version()
	base := ver
	if r.Intn(8) == 0 {
		base = ver - r.Intn(3) - 1 // 故意过期
		if base < 0 {
			base = ver + 1
		}
	}

	cu := m.svc.testCU()
	nedits := 1 + r.Intn(4)
	// 随机选不重叠的码元区间：把文档切成若干锚点再选段。
	anchors := randomSortedPoints(r, cu, nedits*2)
	var edits []Edit
	overlap := r.Intn(12) == 0
	for i := 0; i < nedits && 2*i+1 < len(anchors); i++ {
		s := anchors[2*i]
		e := anchors[2*i+1]
		if overlap && i == 0 && nedits > 1 {
			e = anchors[1] + 1 // 制造正长度重叠（越界风险由锚点保证 e<=cu+?）
			if e > cu {
				e = cu
			}
		}
		// 用朴素码元坐标转位置（ASCII/码元偏移即列，需要真实换行；直接用 svc 换算）。
		sp, errS := m.svc.OffsetToPosition(s)
		ep, errE := m.svc.OffsetToPosition(e)
		if errS != nil || errE != nil {
			// 命中代理对中间：直接把该位置作为（应被拒绝的）输入记录。
			sp = forcePosition(m.naive, s)
			ep = forcePosition(m.naive, e)
		}
		text := randRuneText(r, 6)
		if r.Intn(4) == 0 {
			text = "" // 删除
		}
		if s == e {
			text = randRuneText(r, 6) // 空范围必须是插入
		}
		edits = append(edits, Edit{Range: Range{Start: sp, End: ep}, Text: text})
	}

	fmt.Fprintf(m.log, "[%d] CHANGE base=%d edits=%d %s\n", step, base, len(edits), describeEdits(edits))
	rs := m.svc.Change(base, edits)
	rn := m.naive.change(base, edits)
	fmt.Fprintf(m.log, "    svc   accepted=%v reason=%v dead=%v\n", rs.Accepted, rs.Reason, rs.Dead)
	fmt.Fprintf(m.log, "    naive accepted=%v reason=%v dead=%v\n", rn.Accepted, rn.Reason, rn.Dead)
	if rs.Accepted != rn.Accepted || errKind(rs.Reason) != errKind(rn.Reason) {
		m.flushAndFail(t, step, "change outcome", rs, rn)
	}
	if rs.Accepted && !sameIntSet(rs.Dead, rn.Dead) {
		m.flushAndFail(t, step, "change dead set", rs.Dead, rn.Dead)
	}
}

func (m *randModel) doRegister(t *testing.T, r *rand.Rand, step int) {
	ver := m.svc.Version()
	base := ver
	if r.Intn(8) == 0 {
		base = ver + 1
	}
	cu := m.svc.testCU()
	s := r.Intn(cu + 2) // 允许越界
	e := s
	if r.Intn(3) != 0 {
		e = r.Intn(cu + 2)
	}
	if r.Intn(6) == 0 {
		e, s = s, e // 可能反向
	}
	d := Diagnostic{
		Range:    Range{Start: forcePosition(m.naive, s), End: forcePosition(m.naive, e)},
		Severity: Severity(r.Intn(4) + 1),
		Message:  fmt.Sprintf("m%d", step),
	}
	fmt.Fprintf(m.log, "[%d] REGISTER base=%d raw=(%d,%d)\n", step, base, s, e)
	seqS, errS := m.svc.Register(base, d)
	seqN, errN := m.naive.register(base, d)
	fmt.Fprintf(m.log, "    svc   seq=%d err=%v | naive seq=%d err=%v\n", seqS, errS, seqN, errN)
	if errKind(errS) != errKind(errN) || (errS == nil && seqS != seqN) {
		m.flushAndFail(t, step, "register",
			fmt.Sprintf("seq=%d err=%v", seqS, errS),
			fmt.Sprintf("seq=%d err=%v", seqN, errN))
	}
}

func (m *randModel) doConversions(t *testing.T, r *rand.Rand, step int) {
	cu := m.svc.testCU()
	for i := 0; i < 4; i++ {
		off := r.Intn(cu + 3)
		ps, es := m.svc.OffsetToPosition(off)
		_, en := m.naive.offsetToPosition(off)
		if errKind(es) != errKind(en) {
			m.flushAndFail(t, step, "offset->pos off",
				fmt.Sprintf("off=%d err=%v", off, es),
				fmt.Sprintf("off=%d err=%v", off, en))
		}
		if es == nil {
			// 反向：从位置回偏移，两模型应一致。
			o1, e1 := m.svc.PositionToOffset(ps)
			o2, e2 := m.naive.positionToOffset(ps)
			if o1 != o2 || errKind(e1) != errKind(e2) {
				m.flushAndFail(t, step, "pos->offset",
					fmt.Sprintf("p=%+v off=%d err=%v", ps, o1, e1),
					fmt.Sprintf("p=%+v off=%d err=%v", ps, o2, e2))
			}
		}
	}
	fmt.Fprintf(m.log, "[%d] CONVERSIONS ok\n", step)
}

func (m *randModel) assertEqual(t *testing.T, step int) {
	snap := m.svc.Snapshot()
	if snap.Text != m.naive.textOrString() {
		m.flushAndFail(t, step, "text", snap.Text, m.naive.textOrString())
	}
	if snap.Version != m.naive.version {
		m.flushAndFail(t, step, "version", snap.Version, m.naive.version)
	}
	gotA := m.svc.testActiveOffsets()
	wantA := m.naive.activeDiags()
	if !sameActive(gotA, wantA) {
		m.flushAndFail(t, step, "active diags", gotA, wantA)
	}
	gotD := m.svc.testDeadMeta()
	wantD := m.naive.deadMeta()
	if !sameMeta(gotD, wantD) {
		m.flushAndFail(t, step, "dead meta", gotD, wantD)
	}
	fmt.Fprintf(m.log, "    state v=%d text=%q active=%d dead=%d OK\n",
		snap.Version, snap.Text, len(wantA), len(wantD))
}

func (m *naiveModel) textOrString() string { return decode16(m.cu) }

func (m *randModel) flushAndFail(t *testing.T, step int, what string, got, want interface{}) {
	t.Helper()
	fmt.Fprintf(m.log, "!!! MISMATCH step=%d %s\n got=%#v\nwant=%#v\n", step, what, got, want)
	if m.logf != nil {
		m.logf.WriteString(m.log.String())
		m.logf.Sync()
	}
	t.Logf("\n%s", m.log.String())
	t.Fatalf("random mismatch step=%d %s (seed in log)", step, what)
}

// randomSortedPoints 从 [0,cu] 中取 2k 个（可能含相邻）升序唯一码元点。
func randomSortedPoints(r *rand.Rand, cu, k int) []int {
	if cu == 0 {
		out := make([]int, k)
		return out
	}
	set := map[int]struct{}{}
	// 可选点总数为 cu+1；不足 k 时有界尝试后用已有点补齐，避免无限循环。
	for tries := 0; len(set) < k && tries < k*4+8; tries++ {
		set[r.Intn(cu+1)] = struct{}{}
	}
	out := make([]int, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Ints(out)
	for len(out) < k {
		out = append(out, r.Intn(cu+1))
		sort.Ints(out)
	}
	return out
}

// forcePosition 直接按朴素码元偏移计算（行，列），不做合法性过滤；
// 用于把可能越界/代理对中间的原始偏移转成协议位置输入。
func forcePosition(m *naiveModel, off int) Position {
	if off < 0 {
		return Position{Line: -1, Character: 0}
	}
	line, lineStart := 0, 0
	for i := 0; i < off && i < len(m.cu); i++ {
		if m.cu[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	col := off - lineStart
	if off > len(m.cu) {
		col = off - lineStart // 保持越界
	}
	return Position{Line: line, Character: col}
}

func describeEdits(edits []Edit) string {
	var parts []string
	for _, e := range edits {
		parts = append(parts, fmt.Sprintf("[(%d,%d),(%d,%d)]=%q",
			e.Range.Start.Line, e.Range.Start.Character,
			e.Range.End.Line, e.Range.End.Character, e.Text))
	}
	return strings.Join(parts, " ")
}

func errKind(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func sameIntSet(a, b []int) bool {
	x := append([]int(nil), a...)
	y := append([]int(nil), b...)
	sort.Ints(x)
	sort.Ints(y)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func sameActive(a []naiveItem, b []naiveItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameMeta(a, b [][3]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping random differential in -short mode")
	}
	dir := "testdata"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "random.log")
	logf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logf.Close()
	for seed := 1; seed <= 200; seed++ {
		runRandomSequence(t, seed, 120, logf)
	}
	t.Logf("random differential log written to %s", logPath)
}
