package semver

// bound 是约束在 release 空间上的边界。
// inclusive=true 时边界点本身满足约束。
type bound struct {
	t         triple
	inclusive bool
}

// excludesAllReleases 报告该约束是否拒绝一切 release 版本。
// 预发布等值约束 =M.m.p-pre 只接受那一个预发布版本。
func (c Constraint) excludesAllReleases() bool {
	return c.Op == OpEQ && c.Ver.hasPre
}

// lowerBound 计算约束在 release 版本上的下界；无下界时 ok=false。
func (c Constraint) lowerBound() (b bound, ok bool) {
	t := versionTriple(c.Ver)
	switch c.Op {
	case OpGE:
		if c.Ver.hasPre {
			// >=M.m.p-pre 对 release 而言从 M.m.p 起即满足。
			return bound{t, true}, true
		}
		return bound{t, true}, true
	case OpGT:
		if c.Ver.hasPre {
			// >M.m.p-pre：M.m.p release 仍满足。
			return bound{t, true}, true
		}
		return bound{t, false}, true
	case OpEQ:
		return bound{t, true}, true // hasPre 时由 excludesAllReleases 提前排除
	default:
		return bound{}, false
	}
}

// upperBound 计算约束在 release 版本上的上界；无上界时 ok=false。
func (c Constraint) upperBound() (b bound, ok bool) {
	t := versionTriple(c.Ver)
	switch c.Op {
	case OpLE:
		if c.Ver.hasPre {
			// release M.m.p 大于 M.m.p-pre，故上界为排他 M.m.p。
			return bound{t, false}, true
		}
		return bound{t, true}, true
	case OpLT:
		return bound{t, false}, true
	case OpEQ:
		return bound{t, true}, true
	default:
		return bound{}, false
	}
}

// maxBound 返回两个下界中更强（更靠右）的一个。
func maxBound(a, b bound) bound {
	c := a.t.cmp(b.t)
	if c > 0 || (c == 0 && !b.inclusive && a.inclusive) {
		return a
	}
	if c < 0 || (c == 0 && !a.inclusive && b.inclusive) {
		return b
	}
	return a // 相同
}

// minBound 返回两个上界中更强（更靠左）的一个。
func minBound(a, b bound) bound {
	c := a.t.cmp(b.t)
	if c < 0 || (c == 0 && !b.inclusive && a.inclusive) {
		return a
	}
	if c > 0 || (c == 0 && !a.inclusive && b.inclusive) {
		return b
	}
	return a
}

// satisfiesLower 报告 release 三元组是否满足下界。
func satisfiesLower(t triple, b bound) bool {
	c := t.cmp(b.t)
	return c > 0 || c == 0 && b.inclusive
}
