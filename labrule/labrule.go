// Package labrule 维护检验项目的危急阈值与严重度分档规则。
package labrule

import "errors"

// ErrInvalid 表示阈值参数非法（low>=high、step 越界、编码为空或重复注册）。
var ErrInvalid = errors.New("labrule: invalid parameter")

// maxStep 是分档步长的上界。
const maxStep = 1_000_000

// Rule 是一个检验项目的危急阈值规则：v <= Low 或 v >= High 为危急。
type Rule struct {
	Low  int64
	High int64
	Step int64
}

// Book 是项目编码到阈值规则的注册表。
type Book struct {
	rules map[string]Rule
}

// NewBook 返回空的规则簿。
func NewBook() *Book {
	return &Book{rules: make(map[string]Rule)}
}

// Add 注册一个项目的阈值规则，要求 low < high 且 1 <= step <= 1e6。
func (b *Book) Add(code string, low, high, step int64) error {
	if code == "" || low >= high || step < 1 || step > maxStep {
		return ErrInvalid
	}
	if _, dup := b.rules[code]; dup {
		return ErrInvalid
	}
	b.rules[code] = Rule{Low: low, High: high, Step: step}
	return nil
}

// Has 报告项目是否已注册。
func (b *Book) Has(code string) bool {
	_, ok := b.rules[code]
	return ok
}

// Severity 计算结果值的严重度。非危急返回 (0, false)；
// 危急时 sev = min(3, 1+floor(x/step))，x 为超出量 low-v 或 v-high。
func (b *Book) Severity(code string, v int64) (int, bool) {
	r, ok := b.rules[code]
	if !ok {
		return 0, false
	}
	var x int64
	switch {
	case v <= r.Low:
		x = r.Low - v
	case v >= r.High:
		x = v - r.High
	default:
		return 0, false
	}
	sev := 1 + x/r.Step
	if sev > 3 {
		sev = 3
	}
	return int(sev), true
}
