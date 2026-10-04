package policy

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"ontology/group"
)

func setup(t *testing.T, gmax int) (*group.State, *Store) {
	t.Helper()
	gs, err := group.New(gmax)
	if err != nil {
		t.Fatal(err)
	}
	st := New()
	for _, g := range []struct {
		name string
		pr   int
	}{{"g1", 10}, {"g2", 10}, {"g3", 20}} {
		if err := gs.AddGroup(g.name, g.pr); err != nil {
			t.Fatal(err)
		}
		st.AddGroup(g.name)
	}
	return gs, st
}

func set(t *testing.T, st *Store, g string, p map[string]Value) {
	t.Helper()
	if err := st.Set(g, p); err != nil {
		t.Fatalf("Set(%s): %v", g, err)
	}
}

func TestValueDistinctions(t *testing.T) {
	if Unset().IsSet() {
		t.Error("Unset must not be set")
	}
	v, ok := String("").Get()
	if !ok || v != "" {
		t.Error("empty string is an ordinary set value")
	}
	if _, ok := Unset().Get(); ok {
		t.Error("Unset Get must report false")
	}
}

func TestEffectiveExampleFromSpec(t *testing.T) {
	gs, st := setup(t, 16)
	set(t, st, group.Star, map[string]Value{"interval": String("300"), "log": String("warn")})
	set(t, st, "g1", map[string]Value{"interval": String("30"), "mode": String("eco")})
	set(t, st, "g2", map[string]Value{"interval": String("60")})
	set(t, st, "g3", map[string]Value{"mode": Unset()})
	if err := gs.AddDevice("d"); err != nil {
		t.Fatal(err)
	}

	got := Effective(gs, st, "d")
	want := map[string]string{"interval": "300", "log": "warn"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("base E=%v want %v", got, want)
	}

	if err := gs.AddMember("g1", "d"); err != nil {
		t.Fatal(err)
	}
	if err := gs.AddMember("g2", "d"); err != nil {
		t.Fatal(err)
	}
	// interval 在 g1、g2 同 pr=10 并列，取字节序小的 g1。
	got = Effective(gs, st, "d")
	want = map[string]string{"interval": "30", "mode": "eco", "log": "warn"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tied E=%v want %v", got, want)
	}

	if err := gs.AddMember("g3", "d"); err != nil {
		t.Fatal(err)
	}
	// mode 被 g3 的 Unset 遮蔽，不回落到 g1 的 "eco"。
	got = Effective(gs, st, "d")
	want = map[string]string{"interval": "30", "log": "warn"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unset shadows E=%v want %v", got, want)
	}
}

func TestEmptyStringVsUnset(t *testing.T) {
	gs, st := setup(t, 16)
	set(t, st, group.Star, map[string]Value{"a": String("star"), "b": String("keep")})
	set(t, st, "g1", map[string]Value{"a": String("")}) // 空串是普通值，遮蔽 star
	set(t, st, "g2", map[string]Value{"a": Unset()})    // 同 pr 与 g1 并列，g1 名字小故不生效
	if err := gs.AddDevice("d"); err != nil {
		t.Fatal(err)
	}
	if err := gs.AddMember("g1", "d"); err != nil {
		t.Fatal(err)
	}
	if err := gs.AddMember("g2", "d"); err != nil {
		t.Fatal(err)
	}
	got := Effective(gs, st, "d")
	want := map[string]string{"a": "", "b": "keep"} // 空串作为普通值保留
	if !reflect.DeepEqual(got, want) {
		t.Errorf("E=%v want %v", got, want)
	}

	// g2 提到 pr 20 后 Unset 遮蔽 g1 的空串，也不回落到 star。
	if err := gs.SetPriority("g2", 20); err != nil {
		t.Fatal(err)
	}
	got = Effective(gs, st, "d")
	want = map[string]string{"b": "keep"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("E=%v want %v", got, want)
	}
}

func TestSetValidation(t *testing.T) {
	st := New()
	if !errors.Is(st.Set("nope", nil), group.ErrNotFound) {
		t.Error("Set on missing group ErrNotFound")
	}
	big := map[string]Value{}
	for i := 0; i < MaxEntries+1; i++ {
		big["k"+strings.Repeat("x", i)] = String("v")
	}
	if !errors.Is(st.Set(group.Star, big), group.ErrInvalid) {
		t.Error("33 keys must be ErrInvalid")
	}
	if !errors.Is(st.Set(group.Star, map[string]Value{"": String("v")}), group.ErrInvalid) {
		t.Error("empty key must be ErrInvalid")
	}
	long := strings.Repeat("k", 65)
	if !errors.Is(st.Set(group.Star, map[string]Value{long: String("v")}), group.ErrInvalid) {
		t.Error("65-byte key must be ErrInvalid")
	}
	// 整份替换：旧键必须消失。
	if err := st.Set(group.Star, map[string]Value{"a": String("1"), "b": Unset()}); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(group.Star, map[string]Value{"a": String("1")}); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Get(group.Star)
	if _, ok := p["b"]; ok {
		t.Error("SetPolicy is full replacement; b must be gone")
	}
}

func TestPriorityTieByteOrder(t *testing.T) {
	gs, st := setup(t, 16)
	set(t, st, "g1", map[string]Value{"k": String("g1")})
	set(t, st, "g2", map[string]Value{"k": String("g2")})
	if err := gs.AddDevice("d"); err != nil {
		t.Fatal(err)
	}
	if err := gs.AddMember("g1", "d"); err != nil {
		t.Fatal(err)
	}
	if err := gs.AddMember("g2", "d"); err != nil {
		t.Fatal(err)
	}
	if got := Effective(gs, st, "d")["k"]; got != "g1" {
		t.Errorf("tie winner=%q want g1", got)
	}
	if err := gs.SetPriority("g2", 11); err != nil {
		t.Fatal(err)
	}
	if got := Effective(gs, st, "d")["k"]; got != "g2" {
		t.Errorf("higher pr winner=%q want g2", got)
	}
}
