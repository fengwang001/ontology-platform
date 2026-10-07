package importguard

// DecisionLogger logs every permission/constraint decision with its inputs,
// output and basis.
type DecisionLogger interface {
	LogDecision(record DecisionRecord)
}

// DecisionRecord captures one judgment: the decision stage, its inputs, the
// output and the basis (which permission entries / constraints were used).
type DecisionRecord struct {
	Stage  string
	Inputs map[string]any
	Output string
	Basis  string
}
