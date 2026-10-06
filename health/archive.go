package health

import "sort"

// archive 单个被保人的既往症档案（编码集合）。
type archive struct {
	codes map[string]struct{}
}

func newArchive() *archive { return &archive{codes: map[string]struct{}{}} }

func (a *archive) add(code string) { a.codes[code] = struct{}{} }

// excluded 判定诊断编码是否被既往症除外：仅检查它自身及其祖先链，
// 每上升一级做一次集合查找。返回查询走过的步数（含自身），用于性能证明。
// 开销只与祖先链长度相关，与目录编码总数及档案中无关编码数无关。
func (a *archive) excluded(code string, cat *Catalog) (bool, int) {
	steps := 0
	for {
		steps++
		if _, ok := a.codes[code]; ok {
			return true, steps
		}
		parent, ok := cat.parentOf(code)
		if !ok || parent == "" {
			return false, steps
		}
		code = parent
	}
}

func (a *archive) snapshot() []string {
	out := make([]string, 0, len(a.codes))
	for code := range a.codes {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}
