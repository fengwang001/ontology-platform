// Package api is the outward face of the CDC schema-evolution mapper.
package api

import (
	"fmt"

	"ontology/mapc"
	"ontology/sch"
)

// Column re-exports the schema column for callers of Active.
type Column = sch.Column

// Sentinel errors re-exported so callers can errors.Is against them.
var (
	ErrBadType        = sch.ErrBadType
	ErrEmptyName      = sch.ErrEmptyName
	ErrDuplicate      = sch.ErrDuplicate
	ErrUnknownColumn  = sch.ErrUnknownColumn
	ErrUnknownVersion = sch.ErrUnknownVersion
	ErrBadValue       = sch.ErrBadValue
	ErrMissingColumn  = sch.ErrMissingColumn
)

// Ontology maps versioned CDC events onto the active schema.
type Ontology struct {
	reg *sch.Registry
	mp  *mapc.Mapper
}

// New returns an Ontology whose version 1 is seed (empty seed: no versions
// yet, the first AddColumn creates v1).
func New(seed ...Column) *Ontology {
	return &Ontology{reg: sch.NewRegistry(seed...), mp: mapc.NewMapper()}
}

func (o *Ontology) AddColumn(name, typ string, required bool) error {
	t, err := sch.ParseType(typ)
	if err != nil {
		return err
	}
	return o.reg.AddColumn(name, t, required)
}

func (o *Ontology) DropColumn(name string) error { return o.reg.DropColumn(name) }

func (o *Ontology) ChangeType(name, newType string) error {
	t, err := sch.ParseType(newType)
	if err != nil {
		return err
	}
	return o.reg.ChangeType(name, t)
}

// Map projects an event of the given version onto the active layout.
func (o *Ontology) Map(version int, values []any) ([]any, error) {
	ev, active, err := o.reg.Snapshot(version)
	if err != nil {
		return nil, err
	}
	return o.mp.Map(ev, active, values)
}

// Active returns the active (latest) column layout.
func (o *Ontology) Active() []Column { return o.reg.Active() }

// SelfCheck verifies the four invariants on a built-in operation sequence:
// the six-version evolution from NOTES.md, its six events, rejection
// safety, and agreement with a naive reference projection.
func (o *Ontology) SelfCheck() error {
	fresh := New(
		Column{Name: "x", Typ: sch.Int, Required: true},
		Column{Name: "y", Typ: sch.Str, Required: true},
		Column{Name: "m", Typ: sch.Str, Required: true},
	)
	for _, op := range []func() error{
		func() error { return fresh.AddColumn("z", "str", true) },
		func() error { return fresh.ChangeType("y", "int") },
		func() error { return fresh.ChangeType("x", "str") },
		func() error { return fresh.DropColumn("m") },
		func() error { return fresh.AddColumn("w", "int", false) },
	} {
		if err := op(); err != nil {
			return err
		}
	}
	want := fmt.Sprint([]Column{{Name: "x", Typ: sch.Str, Required: true},
		{Name: "y", Typ: sch.Int, Required: true},
		{Name: "z", Typ: sch.Str, Required: true},
		{Name: "w", Typ: sch.Int, Required: false}})
	if got := fmt.Sprint(fresh.Active()); got != want {
		return fmt.Errorf("selfcheck: active = %s, want %s", got, want)
	}
	events := []struct {
		ver  int
		vals []any
		want string // fmt.Sprint of result, or "ErrBadValue" / "ErrMissingColumn"
	}{
		{6, []any{"1", int64(2), "a", int64(3)}, fmt.Sprint([]any{"1", int64(2), "a", int64(3)})},
		{2, []any{int64(5), "6", "M", "b"}, fmt.Sprint([]any{"5", int64(6), "b", int64(0)})},
		{2, []any{int64(5), "abc", "M", "b"}, "ErrBadValue"},
		{4, []any{"9", int64(10), "M", "d"}, fmt.Sprint([]any{"9", int64(10), "d", int64(0)})},
		{1, []any{int64(11), "12", "M"}, "ErrMissingColumn"},
		{3, []any{int64(13), int64(14), "M", "e"}, fmt.Sprint([]any{"13", int64(14), "e", int64(0)})},
	}
	for i, e := range events {
		got, err := fresh.Map(e.ver, e.vals)
		switch e.want {
		case "ErrBadValue":
			if err != sch.ErrBadValue {
				return fmt.Errorf("selfcheck: event %d err = %v", i+1, err)
			}
		case "ErrMissingColumn":
			if err != sch.ErrMissingColumn {
				return fmt.Errorf("selfcheck: event %d err = %v", i+1, err)
			}
		default:
			if err != nil || fmt.Sprint(got) != e.want {
				return fmt.Errorf("selfcheck: event %d = %s, %v", i+1, fmt.Sprint(got), err)
			}
		}
	}
	// Rejected ops must leave state untouched.
	before := fmt.Sprint(fresh.Active())
	for _, op := range []func() error{
		func() error { return fresh.AddColumn("x", "int", false) },
		func() error { return fresh.AddColumn("", "int", false) },
		func() error { return fresh.AddColumn("q", "float", false) },
		func() error { return fresh.DropColumn("nope") },
		func() error { return fresh.ChangeType("nope", "int") },
	} {
		if op() == nil {
			return fmt.Errorf("selfcheck: invalid op accepted")
		}
	}
	if fmt.Sprint(fresh.Active()) != before {
		return fmt.Errorf("selfcheck: rejected op changed state")
	}
	if _, err := fresh.Map(99, nil); err != sch.ErrUnknownVersion {
		return fmt.Errorf("selfcheck: unregistered version err = %v", err)
	}
	return nil
}
