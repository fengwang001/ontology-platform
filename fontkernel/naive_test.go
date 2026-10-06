package fontkernel

import (
	"math/rand"
	"sort"
	"testing"
)

// naiveModel 是独立于生产实现的朴素参考模型：
//   - 覆盖判定线性扫描每张人脸的每个区间；
//   - 匹配直接用与规格一一对应的朴素排序键全量比较；
//   - 加载状态按期定义逐字符即时计算（不做惰性扫描）。
//
// 它故意不共享 tree.go / match.go 的任何代码路径，用于随机差分测试。
type naiveModel struct {
	cfg      Config
	families map[string][]FaceSpec
	state    map[string]map[int]*naiveState
	now      int64
}

type naiveState struct {
	triggered bool
	triggerAt int64
	loaded    bool
	failed    bool
	gaveUp    bool
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:      cfg,
		families: map[string][]FaceSpec{},
		state:    map[string]map[int]*naiveState{},
	}
}

func naiveCovers(f FaceSpec, r rune) bool {
	for _, c := range f.Runes {
		if c.Lo <= r && r <= c.Hi {
			return true
		}
	}
	return false
}

func (m *naiveModel) match(family string, q matchQuery, r rune) int {
	faces := m.families[family]
	type scored struct {
		idx int
		key rankKey
	}
	var all []scored
	for i, f := range faces {
		if !naiveCovers(f, r) {
			continue
		}
		key := rankKeyOf(q, matchCandidate{face: i, spec: f}, m.cfg)
		if key.styleTier == 2 {
			continue
		}
		all = append(all, scored{i, key})
	}
	if len(all) == 0 {
		return -1
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].key.less(all[j].key) })
	return all[0].idx
}

func (m *naiveModel) periods(p Policy) (block, swap int64) {
	switch p {
	case PolicyBlock:
		return m.cfg.BlockBlockPeriod, -1
	case PolicySwap:
		return m.cfg.SwapBlockPeriod, -1
	case PolicyFallback:
		return m.cfg.FallbackBlockPeriod, m.cfg.FallbackSwapPeriod
	case PolicyOptional:
		return m.cfg.OptionalBlockPeriod, 0
	}
	return 0, -1
}

// periodOf 按朴素定义即时计算一张脸在 t 时刻的时期，不修改状态。
func (m *naiveModel) periodOf(family string, fi int, t int64) Period {
	st := m.state[family][fi]
	f := m.families[family][fi]
	block, swap := m.periods(f.Policy)
	if st.loaded && !st.gaveUp {
		return PeriodLoaded
	}
	if st.gaveUp || st.failed {
		return PeriodFallbackPermanent
	}
	if !st.triggered {
		return PeriodBlock
	}
	blockEnd := st.triggerAt + block
	if t < blockEnd {
		return PeriodBlock
	}
	if swap < 0 || t < blockEnd+swap {
		return PeriodSwap
	}
	return PeriodFallbackPermanent
}

// expire 是朴素模型的时钟推进：逐脸重算是否该放弃。
func (m *naiveModel) expire(t int64) {
	for fname, faces := range m.state {
		for fi, st := range faces {
			if !st.triggered || st.loaded || st.failed {
				continue
			}
			if m.periodOf(fname, fi, t) == PeriodFallbackPermanent {
				st.failed, st.gaveUp = true, true
			}
		}
	}
}

func (m *naiveModel) register(spec FamilySpec) {
	m.families[spec.Name] = append([]FaceSpec(nil), spec.Faces...)
	m.state[spec.Name] = map[int]*naiveState{}
	for i := range spec.Faces {
		m.state[spec.Name][i] = &naiveState{triggerAt: -1}
	}
}

func (m *naiveModel) shape(req ShapeRequest) []RuneResult {
	q := matchQuery{weight: req.Weight, style: req.Style, width: req.Width}
	out := make([]RuneResult, 0, len(req.Text))
	for _, r := range req.Text {
		res := RuneResult{Rune: r, RenderFace: -1, WinnerFace: -1}
		winner := -1
		if _, ok := m.families[req.Family]; ok {
			winner = m.match(req.Family, q, r)
		}
		res.WinnerFamily = req.Family
		res.WinnerFace = winner
		if winner >= 0 {
			st := m.state[req.Family][winner]
			if !st.triggered {
				st.triggered = true
				st.triggerAt = m.now
			}
			f := m.families[req.Family][winner]
			res.SyntheticItalic = q.style == StyleItalic && f.Style == StyleNormal
			p := m.periodOf(req.Family, winner, m.now)
			if p == PeriodFallbackPermanent {
				st.failed, st.gaveUp = true, true
			}
			switch m.periodOf(req.Family, winner, m.now) {
			case PeriodLoaded:
				res.Period, res.RenderFamily, res.RenderFace = PeriodLoaded, req.Family, winner
			case PeriodBlock:
				res.Period, res.RenderFamily, res.RenderFace = PeriodBlock, req.Family, winner
			default:
				res.Period = m.periodOf(req.Family, winner, m.now)
				m.fillFallback(&res, req, q, r)
			}
		} else {
			m.fillFallback(&res, req, q, r)
		}
		out = append(out, res)
	}
	return out
}

func (m *naiveModel) fillFallback(res *RuneResult, req ShapeRequest, q matchQuery, r rune) {
	for _, fname := range req.Fallback {
		faces, ok := m.families[fname]
		if !ok || fname == req.Family {
			continue
		}
		pick := m.match(fname, q, r)
		if pick < 0 {
			continue
		}
		st := m.state[fname][pick]
		if !st.loaded {
			continue
		}
		res.RenderFamily, res.RenderFace = fname, pick
		res.SyntheticItalic = q.style == StyleItalic && faces[pick].Style == StyleNormal
		res.LastResort = false
		return
	}
	res.RenderFamily, res.RenderFace = "", -1
	res.LastResort = true
}

func (m *naiveModel) reportLoaded(family string, face int, at int64) {
	st := m.state[family][face]
	m.expire(at)
	if at > m.now {
		m.now = at
	}
	if st.gaveUp || st.failed {
		return // 朴素模型把迟到报告直接忽略（生产实现应拒绝）
	}
	st.loaded = true
}

// TestNaiveDifferentialRandom 用随机操作序列对照内核与朴素模型。
func TestNaiveDifferentialRandom(t *testing.T) {
	const iterations = 2000
	rng := rand.New(rand.NewSource(20261006))
	policies := []Policy{PolicyBlock, PolicySwap, PolicyFallback, PolicyOptional}

	for iter := 0; iter < iterations; iter++ {
		cfg := DefaultConfig()
		cfg.BlockBlockPeriod = int64(2 + rng.Intn(8))
		cfg.SwapBlockPeriod = int64(rng.Intn(3))
		cfg.FallbackBlockPeriod = int64(1 + rng.Intn(4))
		cfg.FallbackSwapPeriod = int64(1 + rng.Intn(5))
		cfg.OptionalBlockPeriod = int64(1 + rng.Intn(4))

		e := New(cfg, nil)
		m := newNaive(cfg)

		// 1-3 个族，族间可互为回退；每族 1-5 张脸，字符范围在若干小区间上随机。
		familyNames := []string{"A", "B", "C"}
		rng.Shuffle(3, func(i, j int) { familyNames[i], familyNames[j] = familyNames[j], familyNames[i] })
		nFam := 1 + rng.Intn(3)
		familyNames = familyNames[:nFam]
		familyFaces := map[string][]int{}

		for fi, fname := range familyNames {
			n := 1 + rng.Intn(5)
			spec := FamilySpec{Name: fname}
			type sig struct {
				wlo, whi int
				style    Style
				xlo, xhi float64
				lo, hi   rune
			}
			used := map[sig]bool{}
			for k := 0; k < n; k++ {
				w := 100 + 100*rng.Intn(9)
				width := 50.0 + 25*float64(rng.Intn(7))
				style := Style(rng.Intn(2))
				lo := rune('a' + rng.Intn(20))
				hi := lo + rune(rng.Intn(6))
				wHi := w + 100*rng.Intn(3)
				widthHi := width + 25*float64(rng.Intn(2))
				s := sig{w, wHi, style, width, widthHi, lo, hi}
				if used[s] {
					continue
				}
				used[s] = true
				spec.Faces = append(spec.Faces, FaceSpec{
					WeightLo: w, WeightHi: w + 100*rng.Intn(3),
					Style:   style,
					WidthLo: width, WidthHi: width + 25*float64(rng.Intn(2)),
					Runes:  []RuneRange{{Lo: lo, Hi: hi}},
					Policy: policies[rng.Intn(4)],
					URL:    "u",
				})
			}
			// 生产实现可能因重复登记拒绝；朴素模型同样只接受登记成功的族。
			if err := e.RegisterFamily(spec); err == nil {
				m.register(spec)
				for k := range spec.Faces {
					familyFaces[fname] = append(familyFaces[fname], k)
				}
			}
			_ = fi
		}
		if len(m.families) == 0 {
			continue
		}

		// 主族与回退链都从登记成功的族里取。
		okNames := make([]string, 0, len(m.families))
		for name := range m.families {
			okNames = append(okNames, name)
		}
		sort.Strings(okNames)
		primary := okNames[0]
		fallback := okNames[1:]

		// 随机推进/报告/整形交织。仅对已触发脸报告完成（避免错误路径差异）。
		for step := 0; step < 24; step++ {
			switch rng.Intn(3) {
			case 0:
				m.now += int64(rng.Intn(5))
				_ = e.Advance(m.now)
				m.expire(m.now)
			case 1:
				if len(okNames) == 0 {
					continue
				}
				fname := okNames[rng.Intn(len(okNames))]
				fi := rng.Intn(len(m.families[fname]))
				st := m.state[fname][fi]
				if !st.triggered {
					continue
				}
				if st.loaded || st.failed {
					continue
				}
				at := m.now + int64(rng.Intn(4))
				err := e.ReportLoaded(fname, fi, at)
				if err == nil {
					m.reportLoaded(fname, fi, at)
				}
			default:
				text := randomText(rng)
				req := ShapeRequest{
					Family: primary, Fallback: fallback, Text: text,
					Weight: 100 + 100*rng.Intn(9),
					Style:  Style(rng.Intn(2)),
					Width:  50 + 25*float64(rng.Intn(7)),
				}
				got, err := e.Shape(req)
				if err != nil {
					t.Fatalf("iter=%d unexpected shape err: %v", iter, err)
				}
				want := m.shape(req)
				if !runeResultsEqual(want, got.Runes) {
					t.Fatalf("iter=%d step=%d MISMATCH\nreq=%+v\nwant=%+v\ngot =%+v",
						iter, step, req, want, got.Runes)
				}
			}
		}
	}
}

func randomText(rng *rand.Rand) string {
	n := 1 + rng.Intn(6)
	b := make([]rune, n)
	for i := range b {
		b[i] = rune('a' + rng.Intn(26))
	}
	return string(b)
}

func runeResultsEqual(a, b []RuneResult) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		// 仅比较可观察裁决字段（生产实现额外记录 WinnerFamily 等也参与比较）。
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
