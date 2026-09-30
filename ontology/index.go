package ontology

// indexAdd 记录 downstream 已物化并读取 input 这一事实。
// 调用方需持锁。
func (p *Planner) indexAdd(input, downstream partKey) {
	set := p.reverseIndex[input]
	if set == nil {
		set = map[partKey]bool{}
		p.reverseIndex[input] = set
	}
	set[downstream] = true
}
