package errors

import (
	"strings"
	"testing"
)

func TestKindsAreDistinguishable(t *testing.T) {
	cases := []struct {
		err  error
		want Kind
	}{
		{InvalidArgument("a", "x"), KindInvalidArgument},
		{PreHook("a", "x"), KindPreHook},
		{PostHook("a", []HookFailure{{TypeName: "T", HookName: "h", Message: "m"}}), KindPostHook},
	}
	for _, c := range cases {
		got, ok := AsKind(c.err)
		if !ok || got != c.want {
			t.Fatalf("AsKind(%v) = %v,%v want %v", c.err, got, ok, c.want)
		}
	}
	if _, ok := AsKind(nil); ok {
		t.Fatal("nil error must not be normalized")
	}
}

func TestPostHookAggregateMessage(t *testing.T) {
	err := PostHook("act", []HookFailure{
		{TypeName: "A", HookName: "h1", Message: "m1"},
		{TypeName: "B", HookName: "h2", Message: "m2"},
	})
	s := err.Error()
	if !strings.Contains(s, "A/h1: m1") || !strings.Contains(s, "B/h2: m2") {
		t.Fatalf("aggregate message missing failures: %s", s)
	}
	if len(err.Failures) != 2 {
		t.Fatalf("failures: want 2, got %d", len(err.Failures))
	}
}
