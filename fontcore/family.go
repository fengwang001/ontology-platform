package fontcore

import "sort"

// face 是登记后的人脸。
type face struct {
	id   int
	spec FaceSpec
	norm []RuneRange
}

// family 是一个字体族的登记表与匹配索引。
type family struct {
	name   string
	faces  []*face
	byName map[string]*face
	// 每个不相交码点段一棵区间树，使不同范围的人脸得以共存：
	// 每张人脸只登记到它自己覆盖的段上。
	segments  []RuneRange
	trees     []*rangeTree
	fallbacks []string
}

// matchKey 是三维匹配的排序键，按宽度→倾斜→字重字典序，
// 同维无歧义，完全并列以登记序兜底。
type matchKey struct {
	widthRank  int
	styleRank  int
	weightGrp  int
	weightDist int
	faceID     int
}

func validateConfig(c Config) error {
	if c.WeightLow > c.WeightHigh {
		return invalidf("weight thresholds reversed: %d > %d", c.WeightLow, c.WeightHigh)
	}
	if c.BlockBlock < 0 || c.SwapBlock < 0 || c.FallbackBlock < 0 ||
		c.FallbackSwap < 0 || c.OptionalBlock < 0 {
		return invalidf("negative period length")
	}
	return nil
}

func validateFace(fs FaceSpec) error {
	if fs.Name == "" {
		return invalidf("face name empty")
	}
	if fs.WeightLo > fs.WeightHi || fs.WeightLo < 0 {
		return invalidf("weight range invalid: %d..%d", fs.WeightLo, fs.WeightHi)
	}
	if fs.WidthLo > fs.WidthHi || fs.WidthLo <= 0 {
		return invalidf("width range invalid: %d..%d", fs.WidthLo, fs.WidthHi)
	}
	if fs.Style != StyleNormal && fs.Style != StyleItalic {
		return invalidf("unknown style %d", fs.Style)
	}
	switch fs.Display {
	case DisplayBlock, DisplaySwap, DisplayFallback, DisplayOptional:
	default:
		return invalidf("unknown display strategy %d", fs.Display)
	}
	if len(fs.Ranges) == 0 {
		return invalidf("face %q has empty range set", fs.Name)
	}
	for _, rr := range fs.Ranges {
		if rr.Lo > rr.Hi || rr.Lo < 0 {
			return invalidf("face %q range invalid: %d..%d", fs.Name, rr.Lo, rr.Hi)
		}
	}
	return nil
}

func newFamily(spec FamilySpec, cfg Config) (*family, error) {
	if spec.Name == "" {
		return nil, invalidf("family name empty")
	}
	if len(spec.Faces) == 0 {
		return nil, invalidf("family %q has no faces", spec.Name)
	}
	fm := &family{name: spec.Name, byName: map[string]*face{}, fallbacks: spec.Fallbacks}
	var allSegs []RuneRange
	normalized := make([][]RuneRange, len(spec.Faces))
	for i, fs := range spec.Faces {
		if err := validateFace(fs); err != nil {
			return nil, err
		}
		if _, dup := fm.byName[fs.Name]; dup {
			return nil, wrapErr(ErrDuplicateRegister, "face name repeated: "+fs.Name)
		}
		norm := mergeRanges(fs.Ranges)
		normalized[i] = norm
		for j := 0; j < i; j++ {
			other := fm.faces[j]
			if fs.WeightLo == other.spec.WeightLo && fs.WeightHi == other.spec.WeightHi &&
				fs.Style == other.spec.Style &&
				fs.WidthLo == other.spec.WidthLo && fs.WidthHi == other.spec.WidthHi &&
				rangesEqual(norm, other.norm) {
				return nil, wrapErr(ErrDuplicateRegister,
					"face identical in weight/style/width/ranges: "+fs.Name+" vs "+other.spec.Name)
			}
		}
		fc := &face{id: i, spec: fs, norm: norm}
		fm.faces = append(fm.faces, fc)
		fm.byName[fs.Name] = fc
		allSegs = append(allSegs, norm...)
	}
	fm.segments = mergeRanges(allSegs)
	// 每张人脸在它覆盖的每个公共段上登记一次（以段内裁剪后的区间）。
	// 同一公共段内同一张人脸至多一条，因此命中 id 天然不重复。
	for _, seg0 := range fm.segments {
		var rs []RuneRange
		var ids []int
		for _, fc := range fm.faces {
			for _, rr := range fc.norm {
				if rr.Hi < seg0.Lo || rr.Lo > seg0.Hi {
					continue
				}
				lo, hi := rr.Lo, rr.Hi
				if lo < seg0.Lo {
					lo = seg0.Lo
				}
				if hi > seg0.Hi {
					hi = seg0.Hi
				}
				rs = append(rs, RuneRange{Lo: lo, Hi: hi})
				ids = append(ids, fc.id)
			}
		}
		// 用局部 id 建区间树，再映射回人脸 id。
		local := newRangeTree(rs)
		fm.trees = append(fm.trees, &rangeTree{root: local.root, segs: local.segs, ids: ids})
	}
	return fm, nil
}

func rangesEqual(a, b []RuneRange) bool {
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

// coveringIDs 返回覆盖码点 r 的人脸 id。利用段二分定位 O(log S)，
// 再在该段区间树中 O(log R + k) 查询；不扫描所有人脸/所有区间。
func (f *family) coveringIDs(r rune, buf []int) []int {
	buf = buf[:0]
	idx := sort.Search(len(f.segments), func(i int) bool { return f.segments[i].Hi >= r })
	if idx >= len(f.segments) || f.segments[idx].Lo > r {
		return buf
	}
	t := f.trees[idx]
	local := t.query(r, buf)
	for _, lid := range local {
		buf = append(buf, t.ids[lid])
	}
	return buf
}

// match 在覆盖 r 的人脸中按请求参数选出胜者。
func (f *family) match(r rune, weight, width int, style Style, low, high int64) (*face, bool) {
	cands := f.coveringIDs(r, make([]int, 0, 8))
	if len(cands) == 0 {
		return nil, false
	}
	best := f.faces[cands[0]]
	bestKey := keyFor(best, weight, width, style, low, high)
	for _, id := range cands[1:] {
		k := keyFor(f.faces[id], weight, width, style, low, high)
		if lessKey(k, bestKey) {
			best, bestKey = f.faces[id], k
		}
	}
	return best, true
}

func lessKey(a, b matchKey) bool {
	if a.widthRank != b.widthRank {
		return a.widthRank < b.widthRank
	}
	if a.styleRank != b.styleRank {
		return a.styleRank < b.styleRank
	}
	if a.weightGrp != b.weightGrp {
		return a.weightGrp < b.weightGrp
	}
	if a.weightDist != b.weightDist {
		return a.weightDist < b.weightDist
	}
	return a.faceID < b.faceID
}

func keyFor(fc *face, weight, width int, style Style, low, high int64) matchKey {
	grp, dist := weightKey(fc, weight, low, high)
	return matchKey{
		widthRank:  widthKey(fc, width),
		styleRank:  styleKey(fc, style),
		weightGrp:  grp,
		weightDist: dist,
		faceID:     fc.id,
	}
}

const normalWidth = 100

// 宽度键：先取与请求“距离”最小；等距时，请求宽于正常（>100）偏向更宽，
// 请求窄于或等于正常（<=100）偏向更窄。
// 编码为 rank = dist*2 + tie(0 优)。
func widthKey(fc *face, width int) int {
	var nearest, tie int
	switch {
	case width < fc.spec.WidthLo:
		nearest = fc.spec.WidthLo
		if width > normalWidth {
			tie = 1 // 候选位于更窄侧，非偏好侧
		}
	case width > fc.spec.WidthHi:
		nearest = fc.spec.WidthHi
		if width <= normalWidth {
			tie = 1 // 候选位于更宽侧，非偏好侧
		}
	default:
		nearest = width
	}
	d := nearest - width
	if d < 0 {
		d = -d
	}
	return d*2 + tie
}

// 倾斜键：完全一致最优（0）；请求斜体时允许正常人脸合成（1）；
// 请求正常而人脸斜体不允许反向（2）。
func styleKey(fc *face, style Style) int {
	if fc.spec.Style == style {
		return 0
	}
	if style == StyleItalic && fc.spec.Style == StyleNormal {
		return 1
	}
	return 2
}

// weightKey 返回 (组, 到最近端点的距离)。组 0 = 请求落在区间内；
// 否则按三段规则给候选排优先级，组内距离近者优先；
// 完全并列时由 matchKey.faceID（登记序）兜底。
// low/high 为可配置下/上阈值（low<=high）。
//
//	w < low  ：先向下最接近，再向上最接近
//	low<=w<=high：先向上到 high，再向下最接近，再向上超过 high
//	w > high ：先向上最接近，再向下最接近
func weightKey(fc *face, weight int, low, high int64) (int, int) {
	s := fc.spec
	if weight >= s.WeightLo && weight <= s.WeightHi {
		return 0, 0
	}
	w := int64(weight)
	above := weight < s.WeightLo // 候选整体位于请求上方
	edge := s.WeightHi
	if above {
		edge = s.WeightLo
	}
	d := edge - weight
	if d < 0 {
		d = -d
	}
	switch {
	case w < low || (w == low && low == high):
		if !above {
			return 1, d // 先向下最接近
		}
		return 2, d // 再向上最接近
	case w > high:
		if above {
			return 1, d // 先向上最接近
		}
		return 2, d // 再向下最接近
	default:
		// low <= w <= high：先向上到 high（上方且起点不高于 high），
		// 再向下，再向上超过 high。low==high 时 w==high 归入“向上”。
		switch {
		case above && int64(edge) <= high:
			return 1, d
		case !above:
			return 2, d
		default:
			return 3, d
		}
	}
}
