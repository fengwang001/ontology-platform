package di

import (
	"errors"
	"testing"
)

func okCtor(deps map[string]any) (any, error) { return "ok", nil }

func reg(name string, lt Lifetime, deps ...string) Registration {
	return Registration{Name: name, Dependencies: deps, Lifetime: lt, Construct: okCtor}
}

func TestFreezeValidationOrder(t *testing.T) {
	t.Run("duplicate reported before missing dep", func(t *testing.T) {
		c := New()
		mustReg(t, c, reg("a", Singleton))
		mustReg(t, c, reg("a", Singleton))
		err := c.Freeze()
		assertKind(t, err, "duplicate registration")
	})

	t.Run("missing dep reported before cycle", func(t *testing.T) {
		c := New()
		mustReg(t, c, reg("a", Singleton, "b", "ghost"))
		mustReg(t, c, reg("b", Singleton, "a"))
		assertKind(t, c.Freeze(), "missing dependency")
	})

	t.Run("cycle reported before capture", func(t *testing.T) {
		c := New()
		mustReg(t, c, reg("a", Singleton, "b"))
		mustReg(t, c, reg("b", Singleton, "a", "sc"))
		mustReg(t, c, reg("sc", Scoped))
		assertKind(t, c.Freeze(), "circular dependency")
	})

	t.Run("singleton through transient captures scoped", func(t *testing.T) {
		c := New()
		mustReg(t, c, reg("s", Singleton, "t"))
		mustReg(t, c, reg("t", Transient, "sc"))
		mustReg(t, c, reg("sc", Scoped))
		assertKind(t, c.Freeze(), "lifetime capture")
	})

	t.Run("singleton through singleton stops, no capture", func(t *testing.T) {
		c := New()
		mustReg(t, c, reg("s1", Singleton, "s2"))
		mustReg(t, c, reg("s2", Singleton))
		if err := c.Freeze(); err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if err := c.Register(reg("x", Singleton)); !errors.Is(err, ErrContainerFrozen) {
			t.Fatalf("expected frozen error, got %v", err)
		}
	})
}

func mustReg(t *testing.T, c *Container, r Registration) {
	t.Helper()
	if err := c.Register(r); err != nil {
		t.Fatalf("register %s: %v", r.Name, err)
	}
}

func assertKind(t *testing.T, err error, kind string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error, got nil", kind)
	}
	var re *RegistrationError
	if !errors.As(err, &re) || re.Kind != kind {
		t.Fatalf("expected %s, got %v", kind, err)
	}
	t.Logf("input validation -> output %q | 判定依据: %s", err, kind)
}
