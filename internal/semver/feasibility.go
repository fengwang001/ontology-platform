package semver

// feasibilityWitness 在合并后的 groups 上寻找一个能被全部约束接受的版本。
// 找不到时 witness 为零值、ok=false。
func feasibilityWitness(groups []group, all []Constraint) (Version, bool) {
	if w, ok := releaseWitness(all); ok {
		return w, true
	}
	return prereleaseWitness(groups, all)
}

// releaseWitness 仅在 release（无预发布）版本中寻找可行点。
func releaseWitness(all []Constraint) (Version, bool) {
	var lo, hi bound
	hasLo, hasHi := false, false
	for _, c := range all {
		if c.excludesAllReleases() {
			return Version{}, false
		}
		if lb, ok := c.lowerBound(); ok {
			if !hasLo {
				lo, hasLo = lb, true
			} else {
				lo = maxBound(lo, lb)
			}
		}
		if ub, ok := c.upperBound(); ok {
			if !hasHi {
				hi, hasHi = ub, true
			} else {
				hi = minBound(hi, ub)
			}
		}
	}
	// 选择满足下界的最小 release；若同时满足上界则可行。
	t := lo.t
	if hasLo && !lo.inclusive {
		t = nextTriple(lo.t)
	}
	if !hasLo {
		t = triple{0, 0, 0}
	}
	if hasHi && !satisfiesUpper(t, hi) {
		return Version{}, false
	}
	return t.version(), true
}

func satisfiesUpper(t triple, b bound) bool {
	c := t.cmp(b.t)
	return c < 0 || c == 0 && b.inclusive
}

// nextTriple 返回字典序上紧随 t 的 release 三元组。
func nextTriple(t triple) triple {
	return triple{t.major, t.minor, t.patch + 1}
}

// prereleaseWitness 按门槛规则在预发布版本中寻找可行点。
func prereleaseWitness(groups []group, all []Constraint) (Version, bool) {
	gated := commonGatedTriples(groups)
	for _, t := range sortedTriples(gated) {
		if w, ok := witnessAtTriple(t, all); ok {
			return w, true
		}
	}
	return Version{}, false
}

// witnessAtTriple 在某个被所有 group 放行的三元组上构造候选预发布版本。
func witnessAtTriple(t triple, all []Constraint) (Version, bool) {
	// release 等值约束排除该三元组上的一切预发布。
	for _, c := range all {
		if c.Op == OpEQ && !c.Ver.hasPre {
			return Version{}, false
		}
	}

	var pinned Version
	hasPinned := false
	var loSeq []string
	for _, c := range all {
		if c.Op == OpEQ && c.Ver.hasPre {
			if versionTriple(c.Ver) != t {
				return Version{}, false
			}
			if hasPinned && Compare(c.Ver, pinned) != 0 {
				return Version{}, false
			}
			pinned, hasPinned = c.Ver, true
		}
		if applies := c.Op == OpGE || c.Op == OpGT; applies && versionTriple(c.Ver) == t && c.Ver.hasPre {
			s := c.Ver.Pre
			if c.Op == OpGT {
				s = nextPreSeq(s)
			}
			loSeq = maxPreSeq(loSeq, s)
		}
	}

	var cand Version
	if hasPinned {
		cand = pinned
	} else {
		seq := loSeq
		if seq == nil {
			seq = []string{"0"}
		}
		cand = Version{Major: t.major, Minor: t.minor, Patch: t.patch, Pre: seq, hasPre: true}
	}
	if constraintsAccept(all, cand) {
		return cand, true
	}
	return Version{}, false
}

func constraintsAccept(all []Constraint, v Version) bool {
	for _, c := range all {
		if !c.match(v) {
			return false
		}
	}
	return true
}

// nextPreSeq 返回严格大于 seq 的最小预发布标识符序列（追加 "0"）。
func nextPreSeq(seq []string) []string {
	out := make([]string, len(seq)+1)
	copy(out, seq)
	out[len(seq)] = "0"
	return out
}

// maxPreSeq 返回两个预发布序列中优先级更高的一个；nil 视为最小。
func maxPreSeq(a, b []string) []string {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if comparePrerelease(a, true, b, true) >= 0 {
		return a
	}
	return b
}
