package semver

// Match 判定版本是否落在范围内。
//
// 预发布门槛：带预发布的版本，只有在每个原始范围（group）中
// 都存在一条操作数与其 major/minor/patch 完全相同、且自身带预发布的
// 约束时，才可能被匹配。
func (r Range) Match(v Version) bool {
	for _, g := range r.groups {
		if v.hasPre && !g.gates[versionTriple(v)] {
			return false
		}
		for _, c := range g.constraints {
			if !c.match(v) {
				return false
			}
		}
	}
	return true
}

func (c Constraint) match(v Version) bool {
	cmp := Compare(v, c.Ver)
	switch c.Op {
	case OpGE:
		return cmp >= 0
	case OpGT:
		return cmp > 0
	case OpLE:
		return cmp <= 0
	case OpLT:
		return cmp < 0
	default: // OpEQ
		return cmp == 0
	}
}

// String 还原约束的原始书写形式。
func (c Constraint) String() string { return c.Raw }

// Operand 返回约束的操作数版本。
func (c Constraint) Operand() Version { return c.Ver }

// Operator 返回约束的操作符。
func (c Constraint) Operator() Op { return c.Op }
