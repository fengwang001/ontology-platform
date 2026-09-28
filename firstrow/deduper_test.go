package firstrow

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func newTestDeduper(limit int) (*Deduper, *bytes.Buffer) {
	var buf bytes.Buffer
	return New(limit, NewLogger(&buf)), &buf
}

func rejectErr(t *testing.T, err error) *RejectedChangeError {
	t.Helper()
	var rerr *RejectedChangeError
	if !errors.As(err, &rerr) {
		t.Fatalf("want *RejectedChangeError, got %v", err)
	}
	return rerr
}

func TestTieBreaksByID(t *testing.T) {
	d, _ := newTestDeduper(10)
	out, err := d.Apply([]Change{
		{Op: Insert, Key: "k", ID: "b", EventTime: 5},
		{Op: Insert, Key: "k", ID: "a", EventTime: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Output{
		{Kind: Upsert, Key: "k", ID: "b", EventTime: 5},
		{Kind: Delete, Key: "k", ID: "b", EventTime: 5},
		{Kind: Upsert, Key: "k", ID: "a", EventTime: 5},
	}
	assertOutputs(t, out, want)

	id, et, ok := d.FirstRow("k")
	if !ok || id != "a" || et != 5 {
		t.Fatalf("first = %q@%d ok=%v, want a@5", id, et, ok)
	}
}

func TestNegativeEventTimeOrdersFirst(t *testing.T) {
	d, _ := newTestDeduper(10)
	out, _ := d.Apply([]Change{
		{Op: Insert, Key: "k", ID: "x", EventTime: 100},
		{Op: Insert, Key: "k", ID: "y", EventTime: -7},
		{Op: Insert, Key: "k", ID: "z", EventTime: -100},
	})
	assertOutputs(t, out, []Output{
		{Kind: Upsert, Key: "k", ID: "x", EventTime: 100},
		{Kind: Delete, Key: "k", ID: "x", EventTime: 100},
		{Kind: Upsert, Key: "k", ID: "y", EventTime: -7},
		{Kind: Delete, Key: "k", ID: "y", EventTime: -7},
		{Kind: Upsert, Key: "k", ID: "z", EventTime: -100},
	})
}
