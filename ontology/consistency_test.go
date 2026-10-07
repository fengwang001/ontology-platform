package ontology_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"ontology/ontology"
)

func TestSingleWriteUpdatesPropertyAndIndexes(t *testing.T) {
	logger := &recordingLogger{}
	p := ontology.New(testSchema(), logger)
	p.CreateObject("o1")

	if err := p.WriteProperty(context.Background(), "name", []ontology.PropertyWrite{{Object: "o1", Value: ontology.Explicit("alice")}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	value, err := p.Get("o1", "name")
	if err != nil || value != ontology.Explicit("alice") {
		t.Fatalf("value=%v err=%v", value, err)
	}
	assertQuery(t, p, "name-exact", "name", ontology.IndexKey{Present: true, Data: "alice"}, []string{"o1"})
	assertQuery(t, p, "name-alias", "name", ontology.IndexKey{Present: true, Data: "alice"}, []string{"o1"})
	assertQuery(t, p, "name-exact", "name", ontology.MissingKey(), nil)
	if p.Clock() != 1 {
		t.Fatalf("clock=%d", p.Clock())
	}
	printEvent(t, logger.snapshot()[0])
}

func TestMissingAndExplicitDefaultAreDistinct(t *testing.T) {
	p := ontology.New(testSchema(), nil)
	p.CreateObject("missing")
	p.CreateObject("default")
	if err := p.WriteProperty(context.Background(), "name", []ontology.PropertyWrite{{Object: "default", Value: ontology.Explicit("")}}); err != nil {
		t.Fatal(err)
	}
	assertQuery(t, p, "name-exact", "name", ontology.MissingKey(), []string{"missing"})
	assertQuery(t, p, "name-exact", "name", ontology.IndexKey{Present: true, Data: ""}, []string{"default"})
}

func TestBatchFailureRollsBackAllObjectsAndIndexes(t *testing.T) {
	logger := &recordingLogger{}
	p := ontology.New(testSchema(), logger)
	p.CreateObject("a")
	p.CreateObject("b")

	err := p.WriteProperty(context.Background(), "broken", []ontology.PropertyWrite{
		{Object: "a", Value: ontology.Explicit("x")},
		{Object: "b", Value: ontology.Explicit("y")},
	})
	var writeErr *ontology.WriteError
	if !errors.As(err, &writeErr) || writeErr.Code != ontology.ErrBatchRollback {
		t.Fatalf("err=%v", err)
	}
	for _, id := range []string{"a", "b"} {
		value, _ := p.Get(id, "broken")
		if value != ontology.Missing() {
			t.Fatalf("%s changed: %v", id, value)
		}
	}
	assertQuery(t, p, "broken-index", "broken", ontology.MissingKey(), []string{"a", "b"})
	if p.Clock() != 0 {
		t.Fatalf("clock changed: %d", p.Clock())
	}
	for _, event := range logger.snapshot() {
		printEvent(t, event)
	}
}

func TestDeleteClearsAllIndexedPropertiesAtomically(t *testing.T) {
	p := ontology.New(testSchema(), nil)
	p.CreateObject("o1")
	if err := p.WriteProperty(context.Background(), "name", []ontology.PropertyWrite{{Object: "o1", Value: ontology.Explicit("alice")}}); err != nil {
		t.Fatal(err)
	}
	if err := p.WriteProperty(context.Background(), "age", []ontology.PropertyWrite{{Object: "o1", Value: ontology.Explicit(30)}}); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteObject(context.Background(), "o1"); err != nil {
		t.Fatal(err)
	}
	var missing *ontology.WriteError
	_, err := p.Get("o1", "name")
	if !errors.As(err, &missing) || missing.Code != ontology.ErrObjectNotFound {
		t.Fatalf("get err=%v", err)
	}
	assertQuery(t, p, "name-exact", "name", ontology.IndexKey{Present: true, Data: "alice"}, nil)
	assertQuery(t, p, "age-exact", "age", ontology.IndexKey{Present: true, Data: "30"}, nil)
}

func TestErrorPriority(t *testing.T) {
	p := ontology.New(testSchema(), nil)
	p.CreateObject("exists")
	cases := []struct {
		name     string
		property string
		writes   []ontology.PropertyWrite
		want     ontology.ErrorCode
	}{
		{"missing object first", "name", []ontology.PropertyWrite{{Object: "missing", Value: ontology.Explicit("x")}}, ontology.ErrObjectNotFound},
		{"unsupported property first", "unknown", []ontology.PropertyWrite{{Object: "exists", Value: ontology.Explicit("x")}}, ontology.ErrPropertyNotIndexed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.WriteProperty(context.Background(), tc.property, tc.writes)
			var writeErr *ontology.WriteError
			if !errors.As(err, &writeErr) || writeErr.Code != tc.want {
				t.Fatalf("err=%v want=%d", err, tc.want)
			}
		})
	}
}

func assertQuery(t *testing.T, p *ontology.Platform, indexName, property string, key ontology.IndexKey, want []string) {
	t.Helper()
	got, err := p.Query(indexName, property, key)
	if err != nil {
		t.Fatalf("query %s: %v", indexName, err)
	}
	sort.Strings(got)
	if len(got) != len(want) || (len(got) > 0 && !reflect.DeepEqual(got, want)) {
		t.Fatalf("query %s key=%v got=%v want=%v", indexName, key, got, want)
	}
}
