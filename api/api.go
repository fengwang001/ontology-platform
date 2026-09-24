// Package api is the public entry point for deterministic CDC tokenization.
package api

import (
	"errors"
	"fmt"
	"maps"
	"reflect"

	"ontology/mask"
	"ontology/tokn"
)

// Event is the public CDC change type.
type Event = mask.Event

var (
	// ErrBadConfig: maxTokens <= 0 or some configured domain name is "".
	ErrBadConfig = errors.New("api: invalid configuration")
	// ErrUnknownTable: the event's table is absent from the config.
	ErrUnknownTable = mask.ErrUnknownTable
	// ErrEventShape: bad Op or missing/mismatched images.
	ErrEventShape = mask.ErrEventShape
	// ErrTokenLimit: accepting the event would grow tables past maxTokens.
	ErrTokenLimit = tokn.ErrTokenLimit
)

// Masker is the concurrency-safe tokenization facade.
type Masker struct {
	m *mask.Masker
	t *tokn.Tables
}

func sp(s string) *string { return &s }

// New validates the table->column->domain config and builds a Masker capped
// at maxTokens total token entries.
func New(domains map[string]map[string]string, maxTokens int) (*Masker, error) {
	if maxTokens <= 0 {
		return nil, ErrBadConfig
	}
	cfg := make(map[string]map[string]string, len(domains))
	for tbl, cols := range domains {
		cp := make(map[string]string, len(cols))
		for col, dom := range cols {
			if dom == "" {
				return nil, ErrBadConfig
			}
			cp[col] = dom
		}
		cfg[tbl] = cp
	}
	t := tokn.NewTables(maxTokens)
	return &Masker{m: mask.NewMasker(cfg, t), t: t}, nil
}

// Mask returns a tokenized copy of ev. Any rejection leaves state untouched.
func (x *Masker) Mask(ev Event) (Event, error) { return x.m.Mask(ev) }

// Size returns the total number of token entries across all domains.
func (x *Masker) Size() int { return x.t.Size() }

// SelfCheck replays the built-in seven-event sequence and verifies the four
// invariants: one-to-one gap-free tokens, naive-reference numbering,
// structural preservation with an untouched input, and event rollback.
func (x *Masker) SelfCheck() error {
	evs := []Event{
		{Table: "users", Op: 'I', After: map[string]*string{"email": sp("a"), "phone": sp("111")}},
		{Table: "orders", Op: 'I', After: map[string]*string{"buyer_email": sp("b"), "amount": sp("9")}},
		{Table: "orders", Op: 'U', Before: map[string]*string{"buyer_email": sp("b"), "amount": sp("9")},
			After: map[string]*string{"buyer_email": sp("b"), "amount": sp("12")}},
		{Table: "users", Op: 'U', Before: map[string]*string{"email": sp("a"), "phone": sp("111")},
			After: map[string]*string{"email": sp("c"), "phone": nil}},
		{Table: "orders", Op: 'U', Before: map[string]*string{"buyer_email": sp("b")},
			After: map[string]*string{"buyer_email": sp("")}},
		{Table: "users", Op: 'I', After: map[string]*string{"email": sp("d"), "phone": sp("222")}},
		{Table: "users", Op: 'I', After: map[string]*string{"email": sp("e"), "phone": nil}},
	}
	type want struct {
		err      error
		size     int
		bef, aft map[string]string
		nulls    []string // columns that must stay NULL in either image
	}
	wants := []want{
		{size: 2, aft: map[string]string{"email": "email#1", "phone": "phone#1"}},
		{size: 3, aft: map[string]string{"buyer_email": "email#2"}},
		{size: 3, bef: map[string]string{"buyer_email": "email#2"}, aft: map[string]string{"buyer_email": "email#2"}},
		{size: 4, bef: map[string]string{"email": "email#1", "phone": "phone#1"},
			aft: map[string]string{"email": "email#3"}, nulls: []string{"phone"}},
		{size: 5, bef: map[string]string{"buyer_email": "email#2"}, aft: map[string]string{"buyer_email": "email#4"}},
		{err: ErrTokenLimit, size: 5},
		{size: 6, aft: map[string]string{"email": "email#5"}, nulls: []string{"phone"}},
	}
	for i, w := range wants {
		ev := evs[i]
		src := cloneEvent(ev)
		out, err := x.Mask(ev)
		if !errors.Is(err, w.err) {
			return fmt.Errorf("step %d: err=%v want %v", i+1, err, w.err)
		}
		if !reflect.DeepEqual(src, ev) {
			return fmt.Errorf("step %d: input event mutated", i+1)
		}
		if x.Size() != w.size {
			return fmt.Errorf("step %d: size=%d want %d", i+1, x.Size(), w.size)
		}
		if err == nil {
			if e := checkImg(ev.Before, out.Before, w.bef, nil); e != nil {
				return fmt.Errorf("step %d before: %w", i+1, e)
			}
			if e := checkImg(ev.After, out.After, w.aft, w.nulls); e != nil {
				return fmt.Errorf("step %d after: %w", i+1, e)
			}
		}
	}
	return nil
}

func cloneEvent(ev Event) Event {
	return Event{Table: ev.Table, Op: ev.Op, Before: maps.Clone(ev.Before), After: maps.Clone(ev.After)}
}

// checkImg verifies column-set preservation, expected tokens, NULL columns,
// and pointer-identical passthrough of every non-tokenized input value.
func checkImg(in, out map[string]*string, tok map[string]string, nulls []string) error {
	if len(in) != len(out) {
		return fmt.Errorf("column set changed: %d -> %d", len(in), len(out))
	}
	for k, want := range tok {
		if p := out[k]; p == nil || *p != want {
			return fmt.Errorf("%s=%v want %s", k, p, want)
		}
	}
	for _, k := range nulls {
		if p, ok := out[k]; !ok || p != nil {
			return fmt.Errorf("%s must stay NULL, got %v", k, p)
		}
	}
	for k, v := range in {
		if _, isTok := tok[k]; !isTok && out[k] != v {
			return fmt.Errorf("non-tokenized column %s altered", k)
		}
	}
	return nil
}
