package conflict

import (
	"testing"

	"ontology/doc"
)

func TestKindDistinct(t *testing.T) {
	cases := []struct {
		k    Kind
		name string
	}{
		{KindField, "field-conflict"},
		{KindDeleteModify, "delete-vs-modify"},
		{KindAddAdd, "add-vs-add"},
	}
	seen := map[Kind]bool{}
	for _, c := range cases {
		if seen[c.k] || c.k.String() != c.name {
			t.Fatalf("kind %v=%q", c.k, c.k.String())
		}
		seen[c.k] = true
	}
}

func TestListSortDeterministic(t *testing.T) {
	l := List{
		{Key: "b", Field: "x", Kind: KindField},
		{Key: "a", Kind: KindDeleteModify},
		{Key: "b", Field: "a", Kind: KindField},
	}
	want := List{l[1], l[2], l[0]}
	l.Sort()
	for i := range want {
		if l[i] != want[i] {
			t.Fatalf("pos %d = %+v want %+v", i, l[i], want[i])
		}
	}
	if !l.Has(KindDeleteModify) || !l.Has(KindField) || l.Has(KindAddAdd) {
		t.Fatal("Has wrong")
	}
}

func TestBuildReportAccessOncePerConflict(t *testing.T) {
	left := doc.Set{
		"dm":  {"a": doc.Number(1)},
		"fc":  {"f": doc.String("x")},
		"aa":  {"f": doc.String("l")},
	}
	right := doc.Set{
		"dm": {"a": doc.Number(2)},
		"fc": {"f": doc.String("y")},
		"aa": {"f": doc.String("r")},
	}
	cs := List{
		{Key: "dm", Kind: KindDeleteModify, LeftAction: ActionDelete, RightAction: ActionModify},
		{Key: "fc", Field: "f", Kind: KindField, LeftAction: ActionModify, RightAction: ActionModify},
		{Key: "aa", Field: "f", Kind: KindAddAdd, LeftAction: ActionAdd, RightAction: ActionAdd},
	}
	r := BuildReport(cs, left, right)
	if r.Len() != len(cs) {
		t.Fatalf("report len %d", r.Len())
	}
	accessed := 0
	for _, it := range r.Items {
		if it.HasLeft || it.HasRight {
			accessed++
		}
	}
	if accessed != len(cs) {
		t.Fatalf("accessed records=%d want %d", accessed, len(cs))
	}
	if !r.Items[0].HasRight || r.Items[0].HasLeft {
		t.Fatal("delete-modify must evidence the modifier side (right)")
	}
}

func TestTypeNames(t *testing.T) {
	c := Conflict{
		LeftValue: doc.String("s"), LeftHas: true,
		RightValue: doc.Number(1), RightHas: true,
	}
	if c.LeftType() != "string" || c.RightType() != "number" {
		t.Fatalf("types %s/%s", c.LeftType(), c.RightType())
	}
	if (Conflict{}).LeftType() != "absent" {
		t.Fatal("missing value must report absent")
	}
}
