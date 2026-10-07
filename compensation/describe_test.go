package compensation

import "fmt"

func describeOps(allOps [][]SubOp) string {
	s := ""
	for i, ops := range allOps {
		s += fmt.Sprintf("[seq %d] ", i)
		for j, op := range ops {
			s += fmt.Sprintf("{%d:%s obj=%s failInv=%v panicInv=%v check=%v} ",
				j, op.Kind, op.ObjectID, op.InjectInverseFailure, op.InjectInversePanic,
				op.Check != nil)
		}
	}
	return s
}
