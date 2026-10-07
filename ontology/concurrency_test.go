package ontology

import (
	"context"
	"sync"
	"testing"
)

func TestConcurrentMutationsAreSerializableAndSafe(t *testing.T) {
	ctx := context.Background()
	gateway := testGateway(t, RejectObject)
	grantType(t, gateway, "writer", Write)
	for id := 0; id < 20; id++ {
		if err := gateway.CreateObject(ctx, CreateObjectInput{
			Type:   "employee",
			ID:     "obj-" + string(rune('a'+id)),
			Fields: map[string]any{"name": "initial"},
		}); err != nil {
			t.Fatal(err)
		}
	}

	var wait sync.WaitGroup
	for id := 0; id < 20; id++ {
		wait.Add(1)
		go func(id int) {
			defer wait.Done()
			_, _ = gateway.WriteObject(ctx, WriteInput{
				ObjectID:      "obj-" + string(rune('a'+id)),
				SchemaVersion: 1,
				Actor:         "writer",
				Fields:        map[string]any{"name": id},
			})
		}(id)
	}
	wait.Wait()

	grantType(t, gateway, "reader", Read)
	for id := 0; id < 20; id++ {
		objectID := "obj-" + string(rune('a'+id))
		read, err := gateway.ReadObject(ctx, objectID, "reader")
		if err != nil {
			t.Fatal(err)
		}
		if read.Object["name"] != id {
			t.Fatalf("%s = %#v", objectID, read.Object)
		}
	}
}

func TestDecisionLogContainsInputOutputReason(t *testing.T) {
	ctx := context.Background()
	gateway := testGateway(t, RejectObject)
	_, err := gateway.WriteObject(ctx, WriteInput{ObjectID: "missing", SchemaVersion: 1, Actor: "x"})
	requireErrorKind(t, err, KindObjectNotFound)
	entries := gateway.DecisionLog()
	if len(entries) == 0 {
		t.Fatal("no decision log entries")
	}
	last := entries[len(entries)-1]
	if last.Input == nil || last.Error == "" || last.Reason == "" {
		t.Fatalf("incomplete log entry: %#v", last)
	}
}
