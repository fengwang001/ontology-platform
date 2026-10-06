package fontcore

import "sort"

// naiveModel 是独立编写的暴力参考实现：
//   - 覆盖判定：线性扫描该族全部人脸、全部区间；
//   - 匹配：对全部候选计算规则键并排序，不借助区间树；
//   - 期与回退：按规范文字逐步推演。
//
// 它刻意不复用 family.go / rangeset.go 的任何索引结构，
// 使得随机差分测试的“一致”具备真正的对照意义。
type naiveModel struct {
	cfg      Config
	now      int64
	families map[string]*naiveFamily
}

type naiveFace struct {
	order int
	spec  FaceSpec
	norm  []RuneRange
}

type naiveFamily struct {
	spec      FamilySpec
	faces     []*naiveFace
	fallbacks []string
	loads     map[string]*faceLoad
}

func newNaiveModel(cfg Config) *naiveModel {
	return &naiveModel{cfg: cfg, families: map[string]*naiveFamily{}}
}

func (m *naiveModel) register(spec FamilySpec) error {
	fm, err := newFamily(spec, m.cfg)
	if err != nil {
		return err
	}
	if _, ok := m.families[spec.Name]; ok {
		return wrapErr(ErrDuplicateRegister, "family: "+spec.Name)
	}
	nf := &naiveFamily{spec: spec, fallbacks: spec.Fallbacks,
		loads: map[string]*faceLoad{}}
	for i, fs := range spec.Faces {
		nf.faces = append(nf.faces, &naiveFace{
			order: i, spec: fs, norm: fm.faces[i].norm,
		})
		nf.loads[fs.Name] = &faceLoad{}
	}
	m.families[spec.Name] = nf
	return nil
}

func (m *naiveModel) advance(t int64) error {
	if t < m.now {
		return wrapErr(ErrClockRewound, "naive")
	}
	m.now = t
	return nil
}

func (m *naiveModel) report(familyName, faceName string, okLoaded bool) error {
	fm, ok := m.families[familyName]
	if !ok {
		return wrapErr(ErrFamilyNotFound, familyName)
	}
	ld, ok := fm.loads[faceName]
	if !ok {
		return wrapErr(ErrFaceNotFound, familyName+"/"+faceName)
	}
	if !ld.triggered {
		return wrapErr(ErrInvalidLoadState, "not triggered")
	}
	if ld.loaded || ld.failed {
		return wrapErr(ErrInvalidLoadState, "settled")
	}
	if okLoaded {
		ld.loaded, ld.loadAt = true, m.now
	} else {
		ld.failed, ld.failAt = true, m.now
	}
	return nil
}

func nCovers(fc *naiveFace, r rune) bool {
	for _, rr := range fc.norm {
		if rr.Lo <= r && r <= rr.Hi {
			return true
		}
	}
	return false
}

// nMatch 枚举所有覆盖人脸并用独立比较函数全序排序，取最小。
func (m *naiveModel) nMatch(fm *naiveFamily, r rune, req shapeReq) *naiveFace {
	var cands []*naiveFace
	for _, fc := range fm.faces {
		if nCovers(fc, r) {
			cands = append(cands, fc)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	sort.SliceStable(cands, func(i, j int) bool {
		return m.nLess(cands[i], cands[j], req)
	})
	return cands[0]
}

func (m *naiveModel) nLess(a, b *naiveFace, req shapeReq) bool {
	if c := nCmp(nWidthKey(a, req.width), nWidthKey(b, req.width)); c != 0 {
		return c < 0
	}
	if c := nCmp(nStyleKey(a, req.style), nStyleKey(b, req.style)); c != 0 {
		return c < 0
	}
	ga, da := nWeightKey(a, req.weight, m.cfg.WeightLow, m.cfg.WeightHigh)
	gb, db := nWeightKey(b, req.weight, m.cfg.WeightLow, m.cfg.WeightHigh)
	if c := nCmp(ga, gb); c != 0 {
		return c < 0
	}
	if c := nCmp(da, db); c != 0 {
		return c < 0
	}
	return a.order < b.order
}

func nCmp(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// 以下三个键函数独立于 family.go 重新书写（同规范、不同代码路径）。
func nWidthKey(fc *naiveFace, width int) int {
	var edge int
	switch {
	case width < fc.spec.WidthLo:
		edge = fc.spec.WidthLo
	case width > fc.spec.WidthHi:
		edge = fc.spec.WidthHi
	default:
		edge = width
	}
	dist := edge - width
	if dist < 0 {
		dist = -dist
	}
	tie := 0
	if width > normalWidth && edge > width {
		tie = 1 // 请求偏宽而候选更窄
	}
	if width <= normalWidth && edge < width {
		tie = 1 // 请求不宽而候选更宽
	}
	return dist*2 + tie
}

func nStyleKey(fc *naiveFace, style Style) int {
	switch {
	case fc.spec.Style == style:
		return 0
	case style == StyleItalic && fc.spec.Style == StyleNormal:
		return 1
	default:
		return 2
	}
}

func nWeightKey(fc *naiveFace, weight int, low, high int64) (int, int) {
	s := fc.spec
	if s.WeightLo <= weight && weight <= s.WeightHi {
		return 0, 0
	}
	var edge int
	if weight < s.WeightLo {
		edge = s.WeightLo
	} else {
		edge = s.WeightHi
	}
	dist := edge - weight
	if dist < 0 {
		dist = -dist
	}
	w := int64(weight)
	switch {
	case w < low || (w == low && low == high):
		if edge > weight {
			return 2, dist // 向上为次选
		}
		return 1, dist // 先向下取最接近
	case w > high:
		if edge > weight {
			return 1, dist
		}
		return 2, dist
	default:
		if edge > weight && int64(edge) <= high {
			return 1, dist
		}
		if edge < weight {
			return 2, dist
		}
		return 3, dist
	}
}

func (m *naiveModel) periods(fc *naiveFace) (int64, int64, bool) {
	c := m.cfg
	switch fc.spec.Display {
	case DisplayBlock:
		return c.BlockBlock, 0, true
	case DisplaySwap:
		return c.SwapBlock, 0, true
	case DisplayFallback:
		return c.FallbackBlock, c.FallbackSwap, true
	default:
		return c.OptionalBlock, 0, false
	}
}

func (m *naiveModel) ready(fc *naiveFace, ld *faceLoad) (bool, bool) {
	blockLen, swapLen, hasSwap := m.periods(fc)
	blockEnd := ld.triggerAt + blockLen
	if ld.failed {
		return false, true
	}
	if ld.loaded {
		if !hasSwap {
			return ld.loadAt < blockEnd, ld.loadAt >= blockEnd
		}
		if fc.spec.Display == DisplayFallback {
			swapEnd := blockEnd + swapLen
			return ld.loadAt < swapEnd, ld.loadAt >= swapEnd
		}
		return true, false
	}
	return false, false
}

func (m *naiveModel) fallback(fm *naiveFamily, r rune, req shapeReq) (string, string, bool) {
	for _, name := range fm.fallbacks {
		fb, ok := m.families[name]
		if !ok {
			continue
		}
		var best *naiveFace
		for _, fc := range fb.faces {
			if !nCovers(fc, r) || !fb.loads[fc.spec.Name].loaded {
				continue
			}
			if best == nil || m.nLess(fc, best, req) {
				best = fc
			}
		}
		if best != nil {
			return name, best.spec.Name, true
		}
	}
	return "", "", false
}

func (m *naiveModel) shape(familyName, text string, req shapeReq) []CharResult {
	fm := m.families[familyName]
	var out []CharResult
	for _, r := range text {
		out = append(out, m.one(fm, r, req))
	}
	return out
}

func (m *naiveModel) one(fm *naiveFamily, r rune, req shapeReq) CharResult {
	fc := m.nMatch(fm, r, req)
	if fc == nil {
		if name, face, ok := m.fallback(fm, r, req); ok {
			return CharResult{Family: name, Face: face, Phase: PhaseSwap,
				SyntheticItalic: req.style == StyleItalic &&
					m.families[name].faces[faceIndex(m, name, face)].spec.Style == StyleNormal}
		}
		return CharResult{Family: fm.spec.Name, Phase: PhaseLastResort}
	}
	ld := fm.loads[fc.spec.Name]
	if !ld.triggered {
		ld.triggered, ld.triggerAt = true, m.now
	}
	synth := req.style == StyleItalic && fc.spec.Style == StyleNormal
	emit := func(phase Phase) CharResult {
		return CharResult{Family: fm.spec.Name, Face: fc.spec.Name,
			Phase: phase, SyntheticItalic: synth}
	}
	ready, permanent := m.ready(fc, ld)
	if ld.failed {
		permanent = true
	}
	if ready {
		return emit(PhasePrimary)
	}
	if permanent {
		if name, face, ok := m.fallback(fm, r, req); ok {
			return CharResult{Family: name, Face: face, Phase: PhaseFailed,
				SyntheticItalic: req.style == StyleItalic &&
					m.families[name].faces[faceIndex(m, name, face)].spec.Style == StyleNormal}
		}
		return emit(PhaseLastResort)
	}
	blockLen, _, _ := m.periods(fc)
	if m.now < ld.triggerAt+blockLen {
		return emit(PhaseBlock)
	}
	if fc.spec.Display == DisplayOptional {
		if name, face, ok := m.fallback(fm, r, req); ok {
			return CharResult{Family: name, Face: face, Phase: PhaseFailed,
				SyntheticItalic: req.style == StyleItalic &&
					m.families[name].faces[faceIndex(m, name, face)].spec.Style == StyleNormal}
		}
		return emit(PhaseLastResort)
	}
	if name, face, ok := m.fallback(fm, r, req); ok {
		return CharResult{Family: name, Face: face, Phase: PhaseSwap,
			SyntheticItalic: req.style == StyleItalic &&
				m.families[name].faces[faceIndex(m, name, face)].spec.Style == StyleNormal}
	}
	return emit(PhaseLastResort)
}

func faceIndex(m *naiveModel, familyName, faceName string) int {
	for i, fc := range m.families[familyName].faces {
		if fc.spec.Name == faceName {
			return i
		}
	}
	return 0
}

func (m *naiveModel) triggerState(familyName, faceName string) (bool, int64) {
	ld := m.families[familyName].loads[faceName]
	return ld.triggered, ld.triggerAt
}
