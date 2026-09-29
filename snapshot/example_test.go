package snapshot_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"ontology/snapshot"
)

func ExampleDiff() {
	oldSnap := []snapshot.Entry{
		{Key: "a", Value: "1"},
		{Key: "b", Value: "2"},
	}
	newSnap := []snapshot.Entry{
		{Key: "b", Value: "20"},
		{Key: "c", Value: "3"},
	}

	logger := log.New(os.Stdout, "", 0)
	ctx := snapshot.WithLogger(context.Background(), logger)

	changes, err := snapshot.Diff(ctx, snapshot.Config{MaxChanges: 100}, oldSnap, newSnap)
	if err != nil {
		fmt.Println("rejected:", err)
		return
	}
	for _, ch := range changes {
		fmt.Printf("CHANGE %s %s\n", ch.Op, ch.Key)
	}
	// Output:
	// diff start: config={MaxChanges:100} oldLen=2 newLen=2
	// step i=0 j=0: old-only key="a" value="1" => delete (old key absent from new snapshot)
	// step i=1 j=0: shared key="b" old="2" new="20" => update (same key, different value)
	// tail: new-only key="c" value="3" => insert (old snapshot exhausted)
	// diff done: 3 change(s) emitted
	// CHANGE delete a
	// CHANGE update b
	// CHANGE insert c
}
