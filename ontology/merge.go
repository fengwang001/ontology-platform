package ontology

// Rule 是可自动合并属性的合并规则。
// 实现必须满足半格（join-semilattice）公理：
//   - 交换律: Join(a, b) == Join(b, a)
//   - 结合律: Join(Join(a, b), c) == Join(a, Join(b, c))
//   - 幂等律: Join(a, a) == a
//
// 满足这三条即可保证：无论并发写入以何种物理顺序到达，
// 最终合并结果完全相同。
type Rule interface {
	Name() string
	// Join 合并当前值与写入声明的新值，返回确定性的合并结果。
	Join(current, declared Value) Value
	// Canonical 返回值的规范形。半格公理在规范形定义域上成立；
	// 写入路径会在判定前把声明值规范形化。
	Canonical(v Value) Value
	// Validate 校验值是否符合该规则的类型要求。
	Validate(v Value) error
}

// 内置合并规则名称。
const (
	RuleMaxInt   = "max_int"
	RuleMinInt   = "min_int"
	RuleSetUnion = "set_union"
	RuleLWW      = "lww"
)

// RuleByName 返回内置规则实例，未知名称返回 nil。
func RuleByName(name string) Rule {
	switch name {
	case RuleMaxInt:
		return maxIntRule{}
	case RuleMinInt:
		return minIntRule{}
	case RuleSetUnion:
		return setUnionRule{}
	case RuleLWW:
		return lwwRule{}
	default:
		return nil
	}
}

// maxIntRule 取历史声明值与当前值的最大者。
type maxIntRule struct{}

func (maxIntRule) Name() string { return RuleMaxInt }

func (maxIntRule) Join(current, declared Value) Value {
	c, err1 := asInt64(current)
	d, err2 := asInt64(declared)
	if err1 != nil || err2 != nil {
		return current
	}
	if d > c {
		return d
	}
	return c
}

func (maxIntRule) Canonical(v Value) Value { return v }

func (maxIntRule) Validate(v Value) error {
	_, err := asInt64(v)
	return err
}

// minIntRule 取历史声明值与当前值的最小者。
type minIntRule struct{}

func (minIntRule) Name() string { return RuleMinInt }

func (minIntRule) Join(current, declared Value) Value {
	c, err1 := asInt64(current)
	d, err2 := asInt64(declared)
	if err1 != nil || err2 != nil {
		return current
	}
	if d < c {
		return d
	}
	return c
}

func (minIntRule) Canonical(v Value) Value { return v }

func (minIntRule) Validate(v Value) error {
	_, err := asInt64(v)
	return err
}

// setUnionRule 求集合并集（grow-only set），输出排序去重。
type setUnionRule struct{}

func (setUnionRule) Name() string { return RuleSetUnion }

func (setUnionRule) Join(current, declared Value) Value {
	c, err1 := asStringSlice(current)
	d, err2 := asStringSlice(declared)
	if err1 != nil || err2 != nil {
		return current
	}
	return unionSorted(c, d)
}

// Canonical 排序去重，使集合值具有唯一表示。
func (setUnionRule) Canonical(v Value) Value {
	s, err := asStringSlice(v)
	if err != nil {
		return v
	}
	return unionSorted(s, nil)
}

func (setUnionRule) Validate(v Value) error {
	_, err := asStringSlice(v)
	return err
}

// lwwRule 按写入内容中声明的逻辑时钟决胜；时钟相同按写入者标识、
// 再按数据字典序决胜。全程不依赖物理到达时刻，结果确定。
type lwwRule struct{}

func (lwwRule) Name() string { return RuleLWW }

func (lwwRule) Join(current, declared Value) Value {
	c, err1 := asLWW(current)
	d, err2 := asLWW(declared)
	if err1 != nil || err2 != nil {
		return current
	}
	if lessLWW(c, d) {
		return d
	}
	return c
}

func (lwwRule) Canonical(v Value) Value { return v }

func (lwwRule) Validate(v Value) error {
	_, err := asLWW(v)
	return err
}

// lessLWW 定义 LWWValue 的全序：先比逻辑时钟，再比写入者，最后比数据。
func lessLWW(a, b LWWValue) bool {
	if a.Clock != b.Clock {
		return a.Clock < b.Clock
	}
	if a.Writer != b.Writer {
		return a.Writer < b.Writer
	}
	return a.Data < b.Data
}
