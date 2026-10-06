package fontkernel

// matchQuery 是一次匹配请求。
type matchQuery struct {
	weight int
	style  Style
	width  float64
}

// matchCandidate 是一个覆盖了目标字符的候选。
type matchCandidate struct {
	face int
	spec FaceSpec
}

// matchFace 在候选集合中按 宽度→倾斜→字重 的字典序选出唯一胜者。
// 三个维度上完全并列的候选由最后的登记序（face 上标）裁决，故结果无歧义。
func matchFace(q matchQuery, cands []matchCandidate, cfg Config) int {
	best := -1
	var bestKey rankKey
	for _, c := range cands {
		k := rankKeyOf(q, c, cfg)
		if k.styleTier == 2 {
			continue // 反向斜体不允许：该候选不可用
		}
		if best == -1 || k.less(bestKey) {
			best, bestKey = c.face, k
		}
	}
	return best
}

// rankKey 是一个候选在三维匹配中的排序键：先宽度，再倾斜，再字重，最后登记序。
type rankKey struct {
	widthDist  float64
	widthSide  int // 等距时的偏向：见 sideOf
	styleTier  int
	weightTier int
	weightDist int
	weightSide int
	order      int
}

func (k rankKey) less(o rankKey) bool {
	if k.widthDist != o.widthDist {
		return k.widthDist < o.widthDist
	}
	if k.widthSide != o.widthSide {
		return k.widthSide < o.widthSide
	}
	if k.styleTier != o.styleTier {
		return k.styleTier < o.styleTier
	}
	if k.weightTier != o.weightTier {
		return k.weightTier < o.weightTier
	}
	if k.weightDist != o.weightDist {
		return k.weightDist < o.weightDist
	}
	if k.weightSide != o.weightSide {
		return k.weightSide < o.weightSide
	}
	return k.order < o.order
}

func rankKeyOf(q matchQuery, c matchCandidate, cfg Config) rankKey {
	s := c.spec
	k := rankKey{order: c.face}

	// —— 第一维：宽度 ——
	k.widthDist, k.widthSide = widthRank(q.width, s.WidthLo, s.WidthHi, cfg.NormalWidth)

	// —— 第二维：倾斜 ——
	// 精确命中为 0；请求斜体而人脸正常（可合成）为 1；
	// 请求正常而人脸斜体（不允许反向合成）不可接受，用最大层排除。
	switch {
	case q.style == s.Style:
		k.styleTier = 0
	case q.style == StyleItalic && s.Style == StyleNormal:
		k.styleTier = 1
	default:
		k.styleTier = 2
	}

	// —— 第三维：字重 ——
	k.weightTier, k.weightDist, k.weightSide = weightRank(q.weight, s.WeightLo, s.WeightHi, cfg)
	return k
}

// widthRank 返回宽度距离与等距偏向侧。
// side 取值 0=候选位于请求偏好的一侧（等距时胜出），1=另一侧。
// 请求宽于正常（>100）偏好更宽（区间在 req 上方）；
// 窄于或等于正常偏好更窄（区间在 req 下方）。
func widthRank(req, lo, hi, normal float64) (float64, int) {
	if lo <= req && req <= hi {
		return 0, 0
	}
	preferWide := req > normal
	if req < lo {
		// 区间整体宽于请求（在 req 上方）：偏好更宽时位于偏好侧。
		if preferWide {
			return lo - req, 0
		}
		return lo - req, 1
	}
	// 区间整体窄于请求（在 req 下方）：偏好更窄时位于偏好侧。
	if preferWide {
		return req - hi, 1
	}
	return req - hi, 0
}

// weightRank 实现字重三段规则，返回 (层, 距离, 等距偏向)。
//
//   - 请求落在区间内：层 0；
//   - req < WeightLow：先向下（层1）再向上（层2）；
//   - req > WeightHigh：先向上（层1）再向下（层2）；
//   - WeightLow <= req <= WeightHigh：先向上到上阈值（层1），
//     再向下（层2，统一按距离），最后向上超过上阈值（层3）。
//
// 同层内距离更近者优先；层与距离完全相同时以登记序裁决（见 rankKey.order）。
func weightRank(req, lo, hi int, cfg Config) (tier, dist, side int) {
	if lo <= req && req <= hi {
		return 0, 0, 0
	}
	if req < cfg.WeightLow {
		if hi < req {
			return 1, req - hi, 0 // 区间在下方：先向下
		}
		return 2, lo - req, 0 // 区间在上方：再向上
	}
	if req > cfg.WeightHigh {
		if lo > req {
			return 1, lo - req, 0 // 区间在上方：先向上
		}
		return 2, req - hi, 0 // 区间在下方：再向下
	}
	// 中段：两阈值之间（含阈值恰等时的唯一边界点，此时中段仅有一个请求值）。
	// 当两阈值恰等（L==H==req）时，中段退化为阈值点本身：
	// 上方区间全部按“先向上”处理，下方区间随后。
	switch {
	case lo > req && (cfg.WeightLow == cfg.WeightHigh || lo <= cfg.WeightHigh):
		return 1, lo - req, 0 // 向上到上阈值（阈值恰等时覆盖全部上方区间）
	case lo > req:
		return 3, lo - req, 0 // 向上超过上阈值
	case hi < req:
		return 2, req - hi, 0 // 向下（含越过下阈值者，同层按距离）
	}
	return 0, 0, 0 // 不会到达：区间不含 req 时必然 hi<req 或 lo>req
}
