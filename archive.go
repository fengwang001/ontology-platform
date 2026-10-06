package ontology

import "sort"

// archive 是单个被保人的既往症档案。
// 档案中编码及其全部下级均被除外；档案只随该被保人的保单链延续。
type archive struct {
	codes map[string]struct{}
}

func newArchive() *archive {
	return &archive{codes: make(map[string]struct{})}
}

func (a *archive) add(code string) {
	a.codes[code] = struct{}{}
}

// excluded 判定诊断编码是否被除外：自身或任一祖先在档案中。
// 先查自身（O(1)），再沿祖先链逐跳查表（每跳 O(1)），
// 总开销为 O(树高)：不随目录编码总数增长，也不遍历档案中
// 与该编码无祖先关系的编码。
func (a *archive) excluded(code string, cat *catalog) bool {
	if _, ok := a.codes[code]; ok {
		return true
	}
	for _, anc := range cat.ancestors(code) {
		if _, ok := a.codes[anc]; ok {
			return true
		}
	}
	return false
}

// excludedProbes 与 excluded 同逻辑，并返回对档案表的探测次数，
// 用于可验证地证明开销只依赖诊断到根的祖先链长度（1 + 树高），
// 不随目录编码总数或档案中无关编码数量增长。
func (a *archive) excludedProbes(code string, cat *catalog) (bool, int) {
	probes := 1
	if _, ok := a.codes[code]; ok {
		return true, probes
	}
	for _, anc := range cat.ancestors(code) {
		probes++
		if _, ok := a.codes[anc]; ok {
			return true, probes
		}
	}
	return false, probes
}

// snapshot 返回档案编码的稳定有序副本，便于测试与日志比对。
func (a *archive) snapshot() []string {
	out := make([]string, 0, len(a.codes))
	for code := range a.codes {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}
