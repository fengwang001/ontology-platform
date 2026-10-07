package ontology

import (
	"errors"
	"strconv"
	"testing"
)

var (
	objT = ObjectType{Name: "doc"}
	dirT = LinkType{Name: "refs", Directed: true}
	undT = LinkType{Name: "mentions", Directed: false}
)

func itoa(i int) string { return strconv.Itoa(i) }

func mustObj(t *testing.T, g *Graph, id ID) {
	t.Helper()
	if _, err := g.AddObject(Object{ID: id, Type: objT}); err != nil {
		t.Fatalf("add object %s: %v", id, err)
	}
}

func mustLink(t *testing.T, g *Graph, id string, lt LinkType, from, to ID) {
	t.Helper()
	if _, err := g.AddLink(Link{ID: ID(id), Type: lt, From: from, To: to}); err != nil {
		t.Fatalf("add link %s: %v", id, err)
	}
}

func grant(t *testing.T, g *Graph, p string, ids ...ID) {
	t.Helper()
	for _, id := range ids {
		if _, err := g.GrantExistence(p, id); err != nil {
			t.Fatalf("grant %s on %s: %v", p, id, err)
		}
	}
}

func objectIDs(snap *Snapshot) []ID {
	out := make([]ID, len(snap.Objects))
	for i, o := range snap.Objects {
		out[i] = o.ID
	}
	return out
}

func linkIDs(snap *Snapshot) []ID {
	out := make([]ID, len(snap.Links))
	for i, l := range snap.Links {
		out[i] = l.ID
	}
	return out
}

func danglingIDs(snap *Snapshot) []ID {
	out := make([]ID, len(snap.Dangling))
	for i, d := range snap.Dangling {
		out[i] = d.LinkID
	}
	return out
}

var _ = errors.Is
