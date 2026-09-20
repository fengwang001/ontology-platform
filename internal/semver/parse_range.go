package semver

import "strings"

// ParseRange 解析空格分隔的多条约束（逻辑与）。
func ParseRange(s string) (Range, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return Range{}, &ParseError{Kind: ErrInvalidRange, Input: s, Msg: "empty range"}
	}
	g := group{raw: s, gates: map[triple]bool{}}
	for _, f := range fields {
		cs, err := parseConstraintToken(f)
		if err != nil {
			return Range{}, &ParseError{Kind: ErrInvalidRange, Input: s, Msg: err.Error()}
		}
		for _, c := range cs {
			g.constraints = append(g.constraints, c)
			if c.gate {
				g.gates[versionTriple(c.Ver)] = true
			}
		}
	}
	return Range{groups: []group{g}}, nil
}

// parseConstraintToken 解析一条约束；脱字符/波浪号会展开成两条。
func parseConstraintToken(tok string) ([]Constraint, error) {
	switch {
	case strings.HasPrefix(tok, "^"):
		return expandTildeCaret(tok, tok[1:], true)
	case strings.HasPrefix(tok, "~"):
		return expandTildeCaret(tok, tok[1:], false)
	}

	op, rest := splitComparator(tok)
	if rest == "" {
		return nil, &ParseError{Kind: ErrInvalidRange, Input: tok, Msg: "missing version"}
	}
	v, err := Parse(rest)
	if err != nil {
		return nil, err
	}
	return []Constraint{{Op: op, Ver: v, Raw: tok, gate: v.hasPre}}, nil
}

func splitComparator(tok string) (Op, string) {
	switch {
	case strings.HasPrefix(tok, ">="):
		return OpGE, tok[2:]
	case strings.HasPrefix(tok, "<="):
		return OpLE, tok[2:]
	case strings.HasPrefix(tok, ">"):
		return OpGT, tok[1:]
	case strings.HasPrefix(tok, "<"):
		return OpLT, tok[1:]
	case strings.HasPrefix(tok, "="):
		return OpEQ, tok[1:]
	default:
		return OpEQ, tok
	}
}

// expandTildeCaret 展开 ^/~ 约束为 >= 下界与 < 上界。
func expandTildeCaret(raw, verText string, caret bool) ([]Constraint, error) {
	v, err := Parse(verText)
	if err != nil {
		return nil, err
	}
	low := Constraint{Op: OpGE, Ver: v, Raw: raw, gate: v.hasPre}
	upper := upperBound(v, caret)
	up := Constraint{Op: OpLT, Ver: upper, Raw: raw}
	return []Constraint{low, up}, nil
}

// upperBound 返回 ^/~ 约束的排他上界（不含预发布）。
func upperBound(v Version, caret bool) Version {
	switch {
	case !caret:
		// ~M.m.p → <M.(m+1).0
		return Version{Major: v.Major, Minor: v.Minor + 1, Patch: 0}
	case v.Major > 0:
		return Version{Major: v.Major + 1, Minor: 0, Patch: 0}
	case v.Minor > 0:
		// ^0.m.p → <0.(m+1).0
		return Version{Major: 0, Minor: v.Minor + 1, Patch: 0}
	default:
		// ^0.0.p → <0.0.(p+1)
		return Version{Major: 0, Minor: 0, Patch: v.Patch + 1}
	}
}
