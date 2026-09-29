package main

import (
	"fmt"
	"os"

	"ontology/ontology"
)

func main() {
	tbl := ontology.NewTable(ontology.Schema{
		Columns:     []string{"id", "v", "tag"},
		PrimaryKey:  "id",
		IndexColumn: "v",
		UniqueIndex: false,
	})
	for id := int64(1); id <= 5; id++ {
		if err := tbl.Insert(id, map[string]int64{"id": id, "v": id, "tag": 0}); err != nil {
			panic(err)
		}
	}

	exec := ontology.NewExecutor(tbl, os.Stdout)

	run := func(name string, stmt ontology.RangeUpdate) {
		fmt.Printf("==== %s ====\n", name)
		res, err := exec.Execute(stmt)
		if err != nil {
			fmt.Printf("error: %v\n", err)
			return
		}
		fmt.Printf("rows_updated=%d entries_examined=%d\n", res.RowsUpdated, res.EntriesExamined)
		for _, row := range tbl.Snapshot() {
			fmt.Printf("  id=%d v=%d tag=%d\n", row.ID, row.Values["v"], row.Values["tag"])
		}
	}

	run("multiply every key in [1,10) by 2 (keys jump ahead of the scan)",
		ontology.RangeUpdate{Lo: 1, Hi: 10,
			Assignments: []ontology.Assignment{{Column: "v", Op: ontology.AssignMul, Value: 2}}})
	run("empty range [10,10)",
		ontology.RangeUpdate{Lo: 10, Hi: 10,
			Assignments: []ontology.Assignment{{Column: "v", Op: ontology.AssignAdd, Value: 1}}})
}
