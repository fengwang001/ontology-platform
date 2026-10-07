package ontology

func (p *Platform) log(op string, input interface{}, basis string, out interface{}) {
	if p.logger != nil {
		p.logger.Log(DecisionLogEntry{Op: op, Input: input, Basis: basis, Out: out})
	}
}

func (p *Platform) logLocked(op string, input interface{}, basis string, out interface{}) {
	p.log(op, input, basis, out)
}
