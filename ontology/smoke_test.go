package ontology

import "testing"

func TestSmokeFIFO(t *testing.T) {
	ops := []Op{
		setCounter(1, "o", 1, 10, 3),
		setCounter(2, "o", 1, 20, 3),
	}
	results, sched := Run(ops, nil)
	ver, _, attrs := sched.executor.FinalState("o")
	t.Logf("results=%v version=%d attrs=%v", results, ver, attrs)
}
