// Command merge-demo demonstrates in-batch merging of partial-column update
// events, printing every step's input, the merged result, and the rationale.
package main

import (
	"fmt"
	"os"

	"ontology/merge"
)

// stdoutLogger prints engine trace lines.
type stdoutLogger struct{}

func (stdoutLogger) Logf(format string, args ...any) {
	fmt.Printf("    [trace] "+format+"\n", args...)
}

func banner(s string) {
	fmt.Printf("\n=== %s ===\n", s)
}

func main() {
	log := stdoutLogger{}
	tbl := merge.NewTable([]string{"id", "name", "note"}, log)

	banner("1. tri-state: absent vs explicit null vs empty string")
	fmt.Printf("    absent=%v null=%v empty=%v distinct=%v\n",
		merge.Absent(), merge.Null(), merge.Str(""),
		!merge.Absent().Equal(merge.Null()) && !merge.Null().Equal(merge.Str("")))
	mustCommit(tbl, []merge.Event{{
		Kind: merge.EventInsert, Key: "alice",
		Columns: merge.Row{"id": merge.Str("alice"), "name": merge.Null(), "note": merge.Str("")},
	}})
	printRow(tbl, "alice")

	banner("2. insert + update in one batch stays a single insert")
	mustCommit(tbl, []merge.Event{
		{Kind: merge.EventInsert, Key: "bob",
			Columns: merge.Row{"id": merge.Str("bob"), "name": merge.Str("Bob"), "note": merge.Str("x")}},
		{Kind: merge.EventUpdate, Key: "bob",
			Columns: merge.Row{"name": merge.Str("Bobby"), "note": merge.Null()},
			Before:  merge.Row{"name": merge.Str("Bob"), "note": merge.Str("x")}},
	})
	printRow(tbl, "bob")

	banner("3. update merge: union of columns, last-wins, first-before, prune no-ops")
	mustCommit(tbl, []merge.Event{
		{Kind: merge.EventUpdate, Key: "alice",
			Columns: merge.Row{"name": merge.Str("Alicia"), "note": merge.Str("n1")},
			Before:  merge.Row{"name": merge.Null(), "note": merge.Str("")}},
		{Kind: merge.EventUpdate, Key: "alice",
			Columns: merge.Row{"note": merge.Str("")}, // back to original => pruned
			Before:  merge.Row{"note": merge.Str("n1")}},
	})
	printRow(tbl, "alice")

	banner("4. illegal inputs are rejected with distinct reasons; table unchanged")
	rejections := []merge.Event{
		{Kind: merge.EventUpdate, Key: "alice", Columns: merge.Row{"nope": merge.Str("1")}, Before: merge.Row{"nope": merge.Str("1")}},
		{Kind: merge.EventUpdate, Key: "ghost", Columns: merge.Row{"name": merge.Str("1")}, Before: merge.Row{"name": merge.Str("1")}},
		{Kind: merge.EventInsert, Key: "alice", Columns: merge.Row{"id": merge.Str("x"), "name": merge.Str("y"), "note": merge.Str("z")}},
		{Kind: merge.EventUpdate, Key: "alice", Columns: merge.Row{"name": merge.Str("q")}, Before: merge.Row{"name": merge.Str("WRONG")}},
	}
	for i, ev := range rejections {
		_, err := tbl.Commit([]merge.Event{ev})
		fmt.Printf("    rejection #%d: %v\n", i+1, err)
	}

	banner("5. self-check")
	if err := tbl.SelfCheck(); err != nil {
		fmt.Println("    self-check FAILED:", err)
		os.Exit(1)
	}
	fmt.Println("    self-check OK; rows =", tbl.Len())
}

func mustCommit(tbl *merge.Table, events []merge.Event) {
	res, err := tbl.Commit(events)
	if err != nil {
		fmt.Println("    unexpected rejection:", err)
		os.Exit(1)
	}
	fmt.Printf("    merged output keys=%v changes=%d\n", res.Keys(), len(res.Changes))
}

func printRow(tbl *merge.Table, key string) {
	row, ok := tbl.Get(key)
	if !ok {
		fmt.Printf("    row %q: <missing>\n", key)
		return
	}
	fmt.Printf("    row %q: id=%v name=%v note=%v\n", key, row["id"], row["name"], row["note"])
}
