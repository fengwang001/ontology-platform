package config

import (
	"errors"
	"testing"
)

func TestCheckParams(t *testing.T) {
	valid := Push{
		ClusterUpserts: []string{"c1"},
		RouteUpserts:   []Route{{Name: "r1", Refs: []string{"c1", "c2"}}},
		ClusterDeletes: []string{"c9"},
		RouteDeletes:   []string{"r9"},
	}
	if err := CheckParams(valid); err != nil {
		t.Fatalf("valid push rejected: %v", err)
	}
	for i, p := range []Push{
		{ClusterUpserts: []string{""}},
		{ClusterUpserts: []string{"c", "c"}},
		{RouteUpserts: []Route{{Name: "", Refs: []string{"c"}}}},
		{RouteUpserts: []Route{{Name: "r", Refs: []string{"c", "c"}}}},
		{RouteUpserts: []Route{{Name: "r", Refs: []string{}}}},
		{RouteUpserts: []Route{{Name: "r", Refs: []string{"c", ""}}}},
		{ClusterDeletes: []string{""}},
		{RouteDeletes: []string{""}},
		{ClusterUpserts: []string{"c"}, ClusterDeletes: []string{"c"}},
		{RouteUpserts: []Route{{Name: "r", Refs: []string{"c"}}}, RouteDeletes: []string{"r"}},
		{RouteUpserts: []Route{
			{Name: "r", Refs: []string{"a"}},
			{Name: "r", Refs: []string{"b"}},
		}},
	} {
		if err := CheckParams(p); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("case %d want invalid, got %v", i, err)
		}
	}
	refs := make([]string, 9)
	for i := range refs {
		refs[i] = "c"
	}
	if err := CheckParams(Push{RouteUpserts: []Route{{Name: "r", Refs: refs}}}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("9 refs want invalid, got %v", err)
	}
}

func TestCheckTime(t *testing.T) {
	if err := CheckTime(5, 5); err != nil {
		t.Fatalf("equal now ok, got %v", err)
	}
	if err := CheckTime(-1, 0); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("negative want invalid time, got %v", err)
	}
	if err := CheckTime(1_000_000_000_000_001, 0); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("too large want invalid time, got %v", err)
	}
	if err := CheckTime(4, 5); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("rewind want err, got %v", err)
	}
}

func TestCheckDanglingAndInUse(t *testing.T) {
	post := map[string]bool{"c1": true, "c2": true}
	if err := CheckDangling([]Route{{Name: "r", Refs: []string{"c1", "c2"}}}, post); err != nil {
		t.Fatalf("refs in set: %v", err)
	}
	if err := CheckDangling([]Route{{Name: "r", Refs: []string{"c3"}}}, post); !errors.Is(err, ErrDanglingRef) {
		t.Fatalf("c3 dangling, got %v", err)
	}

	routes := map[string]ExistingRoute{
		"serve": {ServingRefs: []string{"c1"}},
		"pend":  {Updated: true, HasPending: true, PendingRefs: []string{"c2"}},
		"gone":  {Deleted: true, ServingRefs: []string{"c3"}},
		"upd":   {Updated: true, ServingRefs: []string{"c3"}, HasPending: true, PendingRefs: []string{"c1"}},
	}
	if err := CheckInUse([]string{"c9"}, routes); err != nil {
		t.Fatalf("unused cluster: %v", err)
	}
	if err := CheckInUse([]string{"c1"}, routes); !errors.Is(err, ErrInUse) {
		t.Fatalf("c1 in use by serving route, got %v", err)
	}
	if err := CheckInUse([]string{"c2"}, routes); !errors.Is(err, ErrInUse) {
		t.Fatalf("c2 in use by pending route, got %v", err)
	}
	if err := CheckInUse([]string{"c3"}, routes); !errors.Is(err, ErrInUse) {
		t.Fatalf("c3 in use by updated route old serving, got %v", err)
	}
	// 删除的路由不参与。
	delete(routes, "upd")
	if err := CheckInUse([]string{"c3"}, routes); err != nil {
		t.Fatalf("c3 only by deleted route, got %v", err)
	}
}
