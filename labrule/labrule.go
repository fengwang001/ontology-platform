package labrule

import "errors"

var (
	// ErrInvalid 阈值或步长参数非法。
	ErrInvalid = errors.New("labrule: invalid argument")
	// ErrDuplicateTest 项目重复登记。
	ErrDuplicateTest = errors.New("labrule: duplicate test code")
	// ErrUnknownTest 项目未登记。
	ErrUnknownTest = errors.New("labrule: unknown test code")
)

type test struct {
	low, high, step int64
}

// Rules 保存全部检验项目的危急阈值与分档步长。
type Rules struct {
	tests map[string]test
}

// NewRules 创建空规则集。
func NewRules() *Rules { return &Rules{tests: make(map[string]test)} }

// Has 报告项目是否已登记。
func (r *Rules) Has(code string) bool {
	_, ok := r.tests[code]
	return ok
}

// AddTest 登记一个检验项目，骨架占位。
// 要求 low<high、step∈[1,10^6]、code 非空且不重复。
func (r *Rules) AddTest(code string, low, high, step int64) error {
	if code == "" || low >= high || step < 1 || step > 1_000_000 {
		return ErrInvalid
	}
	if _, dup := r.tests[code]; dup {
		return ErrDuplicateTest
	}
	r.tests[code] = test{low: low, high: high, step: step}
	return nil
}

// Critical 判定结果是否危急。
func (r *Rules) Critical(code string, v int64) bool {
	_, _, _, ok := r.eval(code, v)
	return ok
}

// Severity 返回危急严重度（1..3），非危急时第二返回值为 false。
func (r *Rules) Severity(code string, v int64) (int, bool) {
	sev, _, crit, ok := r.eval(code, v)
	if !ok || !crit {
		return 0, false
	}
	return sev, true
}

func (r *Rules) eval(code string, v int64) (sev int, excess int64, crit, known bool) {
	t, ok := r.tests[code]
	if !ok {
		return 0, 0, false, false
	}
	switch {
	case v <= t.low:
		excess = t.low - v
	case v >= t.high:
		excess = v - t.high
	default:
		return 0, 0, false, true
	}
	s := int(1 + excess/t.step)
	if s > 3 {
		s = 3
	}
	return s, excess, true, true
}
