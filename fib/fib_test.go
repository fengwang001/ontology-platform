package fib

import (
	"errors"
	"testing"

	"ontology/bucket"
	"ontology/nhgroup"
)

func spec(nexthop uint32) bucket.MemberSpec {
	return bucket.MemberSpec{Nexthop: nexthop, Weight: 1, Alive: true}
}

func TestLongestPrefixFallbackAndErrors(t *testing.T) {
	controller := bucket.NewController()
	f := New(controller)
	if err := controller.CreateGroup(1, 4, 0, 0, []bucket.MemberSpec{spec(1)}, 0); err != nil {
		t.Fatal(err)
	}
	if err := controller.CreateGroup(2, 4, 0, 0, []bucket.MemberSpec{spec(2)}, 0); err != nil {
		t.Fatal(err)
	}
	a := Prefix{Address: 0x0a000000, Length: 8}
	b := Prefix{Address: 0x0a010000, Length: 16}
	if err := f.AddRoute(a, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := f.AddRoute(b, 2, 2); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Route(0x0a010203, 0, 3); err != nil || got != 2 {
		t.Fatalf("longest route = (%d, %v), want 2", got, err)
	}
	if err := controller.NexthopDown(2, 4); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Route(0x0a010203, 0, 5); err != nil || got != 1 {
		t.Fatalf("fallback route = (%d, %v), want 1", got, err)
	}
	if err := controller.NexthopDown(1, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Route(0x0a010203, 0, 7); !errors.Is(err, nhgroup.ErrNoNexthop) {
		t.Fatalf("all-dead route error = %v, want no nexthop", err)
	}
	if _, err := f.Route(0x0b000001, 0, 8); !errors.Is(err, nhgroup.ErrNoRoute) {
		t.Fatalf("uncovered route error = %v, want no route", err)
	}
}

func TestRouteValidationAndReferences(t *testing.T) {
	controller := bucket.NewController()
	f := New(controller)
	if err := controller.CreateGroup(1, 2, 0, 0, []bucket.MemberSpec{spec(1)}, 0); err != nil {
		t.Fatal(err)
	}
	hostBits := Prefix{Address: 0x0a000001, Length: 8}
	if err := f.AddRoute(hostBits, 1, 0); !errors.Is(err, nhgroup.ErrInvalidArgument) {
		t.Fatalf("host bits error = %v", err)
	}
	valid := Prefix{Address: 0x0a000000, Length: 8}
	if err := f.AddRoute(valid, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := f.AddRoute(valid, 1, 1); !errors.Is(err, nhgroup.ErrAlreadyExists) {
		t.Fatalf("duplicate route error = %v", err)
	}
	if err := controller.DeleteGroup(1, 2); !errors.Is(err, nhgroup.ErrInUse) {
		t.Fatalf("delete used group error = %v", err)
	}
	if err := f.DelRoute(valid, 3); err != nil {
		t.Fatal(err)
	}
	if err := controller.DeleteGroup(1, 4); err != nil {
		t.Fatalf("delete unreferenced group: %v", err)
	}
	if err := f.DelRoute(valid, 5); !errors.Is(err, nhgroup.ErrNotFound) {
		t.Fatalf("delete missing route error = %v", err)
	}
}
