package alarm

// recomputeSuppressed 依据当前为真的工况集合重算该点是否处于条件抑制：
// 任一配置工况为真即被抑制。
func (p *activePoint) recomputeSuppressed(active map[string]struct{}) {
	p.suppressed = false
	for c := range p.conds {
		if _, ok := active[c]; ok {
			p.suppressed = true
			return
		}
	}
}
