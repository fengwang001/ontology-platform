package semver

// Op 是单条版本约束的比较操作符。
type Op uint8

const (
	OpGE Op = iota // >=
	OpGT           // >
	OpLE           // <=
	OpLT           // <
	OpEQ           // =
)

// Constraint 是范围里的一条原子约束，如 >=1.2.3。
type Constraint struct {
	Op   Op
	Ver  Version
	Raw  string // 用户书写的原始形式，用于错误提示
	gate bool   // 操作数自身带预发布
}

// group 对应一次 ParseRange 产生的一条 Range：
// 组内约束为逻辑与，组自带的预发布门槛集合仅在本组生效。
type group struct {
	constraints []Constraint
	gates       map[triple]bool
	raw         string
}

// Range 是若干 group 的逻辑与（Intersect 的结果）。
type Range struct {
	groups []group
}

// Raw 返回该 Range 的原始约束文本（单组时为用户输入）。
func (r Range) Raw() string {
	if len(r.groups) == 1 {
		return r.groups[0].raw
	}
	out := ""
	for i, g := range r.groups {
		if i > 0 {
			out += " "
		}
		out += "(" + g.raw + ")"
	}
	return out
}

// Constraints 返回交集中所有原子约束的副本。
func (r Range) Constraints() []Constraint {
	var out []Constraint
	for _, g := range r.groups {
		out = append(out, g.constraints...)
	}
	return out
}

// triple 是不带预发布的版本三元组。
type triple struct {
	major, minor, patch int
}

func versionTriple(v Version) triple {
	return triple{v.Major, v.Minor, v.Patch}
}

func (t triple) version() Version {
	return Version{Major: t.major, Minor: t.minor, Patch: t.patch}
}

func (t triple) cmp(o triple) int {
	if c := compareInt(t.major, o.major); c != 0 {
		return c
	}
	if c := compareInt(t.minor, o.minor); c != 0 {
		return c
	}
	return compareInt(t.patch, o.patch)
}
