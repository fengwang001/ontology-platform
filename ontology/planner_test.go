package ontology

import (
	"bytes"
	"errors"
	"testing"
)

func reasonOf(err error) Reason {
	var e *Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return ""
}

func newTestPlanner(t *testing.T, assets []AssetSpec, edges []EdgeSpec) (*Planner, *bytes.Buffer) {
	t.Helper()
	var log bytes.Buffer
	p, err := New(assets, edges, WithLogOutput(&log))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, &log
}

func basicGraph(t *testing.T) (*Planner, *bytes.Buffer) {
	return newTestPlanner(t,
		[]AssetSpec{
			{Name: "S", First: 0, Last: 9},
			{Name: "A", First: 0, Last: 9},
			{Name: "B", First: 0, Last: 9},
		},
		[]EdgeSpec{
			{Up: "S", Down: "A", Lo: 0, Hi: 0},
			{Up: "A", Down: "B", Lo: 0, Hi: 0},
		})
}

func mustStart(t *testing.T, p *Planner, asset string, part int) int64 {
	t.Helper()
	id, err := p.Start(asset, part)
	if err != nil {
		t.Fatalf("Start %s#%d: %v", asset, part, err)
	}
	return id
}
