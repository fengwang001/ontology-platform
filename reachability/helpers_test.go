package reachability

import (
	"errors"
	"testing"
)

func mustAdd(t *testing.T, g *Graph, from, to string) {
	t.Helper()
	if _, err := g.AddEdge(from, to); err != nil {
		t.Fatalf("AddEdge(%q,%q) unexpected error: %v", from, to, err)
	}
}

func mustAddN(t *testing.T, g *Graph, from, to string, n uint64) {
	t.Helper()
	if _, err := g.AddEdgeN(from, to, n); err != nil {
		t.Fatalf("AddEdgeN(%q,%q,%d) unexpected error: %v", from, to, n, err)
	}
}

func mustRemove(t *testing.T, g *Graph, from, to string) {
	t.Helper()
	if _, err := g.RemoveEdge(from, to); err != nil {
		t.Fatalf("RemoveEdge(%q,%q) unexpected error: %v", from, to, err)
	}
}

func assertErrKind(t *testing.T, err error, kind ErrorKind, sentinel error, ctx string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error %s, got nil", ctx, kind)
	}
	var opErr *OpError
	if !errors.As(err, &opErr) || opErr.Kind != kind {
		t.Fatalf("%s: error kind = %v, want %s (err=%v)", ctx, err, kind, err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("%s: errors.Is(err, sentinel) = false for err=%v", ctx, err)
	}
}
