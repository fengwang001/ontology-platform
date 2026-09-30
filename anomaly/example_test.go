package anomaly_test

import (
	"fmt"

	"ontology/anomaly"
)

func ExampleAnalyze_writeSkew() {
	v0 := anomaly.Version{Txn: 0, Seq: 0}
	h := anomaly.History{
		Txns: []anomaly.Txn{
			{ID: 1, Status: anomaly.Committed, Ops: []anomaly.Op{
				{Key: "x", Read: &v0},
				{Key: "y"},
			}},
			{ID: 2, Status: anomaly.Committed, Ops: []anomaly.Op{
				{Key: "y", Read: &v0},
				{Key: "x"},
			}},
		},
		Order: map[string][]anomaly.Version{
			"x": {{Txn: 0, Seq: 0}, {Txn: 2, Seq: 1}},
			"y": {{Txn: 0, Seq: 0}, {Txn: 1, Seq: 1}},
		},
	}
	r := anomaly.Analyze(h)
	fmt.Println(r.Category, r.Level, r.Witness)
	// Output:
	// G2 PL-2+ [1 2]
}
