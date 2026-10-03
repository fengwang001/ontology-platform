package guard

import (
	"errors"
	"testing"
)

func TestGuardTripAndRelease(t *testing.T) {
	g := New()
	if g.Suspected() {
		t.Fatal("initial guard must not be suspect")
	}
	// 严格大于：相等不熔断；n0=0 不熔断。
	if g.ShouldTrip(1, 4, 25) { // 100 == 100
		t.Fatal("equal must not trip")
	}
	if g.ShouldTrip(0, 0, 50) {
		t.Fatal("empty partition must not trip")
	}
	if !g.ShouldTrip(2, 4, 25) { // 200 > 100
		t.Fatal("strictly greater must trip")
	}
	// 非 Suspect 放行报错；权限不足优先。
	if err := g.Release(2); !errors.Is(err, ErrNotSuspect) {
		t.Fatalf("release non-suspect err=%v want ErrNotSuspect", err)
	}
	g.Trip()
	if err := g.Release(1); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("release role=1 err=%v want ErrUnauthorized", err)
	}
	if !g.Suspected() {
		t.Fatal("rejected release must keep Suspect")
	}
	if err := g.Release(2); err != nil || g.Suspected() {
		t.Fatalf("release role=2 err=%v suspect=%v", err, g.Suspected())
	}
}
