// Package api is the public facade of the incremental 3D CUBE; it depends
// only on package cube (which depends on dim), never the reverse.
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/cube"
	"ontology/dim"
)

// Fact is one upstream fact; the empty string "" is a legal concrete value.
type Fact struct {
	A, B, C string
	V       int64
}

// Cell is an alias of cube.Cell: one non-empty cell, key plus signed sum.
type Cell = cube.Cell

// Re-exported pairwise-distinct sentinel errors, detectable with errors.Is.
var (
	ErrInvalidMaxCells = cube.ErrInvalidMaxCells
	ErrCellLimit       = cube.ErrCellLimit
	ErrFactNotFound    = cube.ErrFactNotFound
)

type Cube struct {
	mu sync.RWMutex
	cb *cube.Cube
}

func New(maxCells int) (*Cube, error) {
	cb, err := cube.New(maxCells)
	if err != nil {
		return nil, err
	}
	return &Cube{cb: cb}, nil
}
func (x *Cube) Add(f Fact) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.cb.Add(f.A, f.B, f.C, f.V)
}
func (x *Cube) Remove(f Fact) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.cb.Remove(f.A, f.B, f.C, f.V)
}
func (x *Cube) View() []Cell {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.cb.View()
}

// Level is the count of concrete (non-ALL) dimensions, always 0..3.
func Level(c Cell) int { return c.Key.Level() }

func viewMap(cs []Cell) map[dim.Key]int64 {
	m := make(map[dim.Key]int64, len(cs))
	for _, c := range cs {
		m[c.Key] = c.Sum
	}
	return m
}

// SelfCheck replays a deterministic sequence on a fresh instance and
// verifies the four invariants; it never mutates x and is concurrency-safe.
func (x *Cube) SelfCheck() error {
	c, err := New(100000)
	if err != nil {
		return err
	}
	model := map[dim.Key]int64{}
	apply := func(f Fact, sign int64) error {
		op := c.Add // method value; sign picks exactly one of Add/Remove
		if sign < 0 {
			op = c.Remove
		}
		if e := op(f); e != nil {
			return e
		}
		for _, key := range dim.Cells(f.A, f.B, f.C) {
			model[key] += sign * f.V
			if model[key] == 0 {
				delete(model, key)
			}
		}
		if !reflect.DeepEqual(viewMap(c.View()), model) {
			return fmt.Errorf("api: batch equivalence mismatch")
		}
		return nil
	}
	stream := []struct {
		f    Fact
		sign int64
	}{
		{Fact{"a", "b", "c", 2}, 1},
		{Fact{"a", "b", "c", 3}, 1},
		{Fact{"a", "d", "c", 5}, 1},
		{Fact{"e", "b", "c", 7}, 1},
		{Fact{"a", "b", "c", 2}, -1}, // step 5 of NOTES.md
		{Fact{"", "b", "c", 4}, 1},
		{Fact{"a", "d", "c", -5}, 1},
		{Fact{"e", "b", "c", -7}, 1},
	}
	for _, op := range stream {
		if err := apply(op.f, op.sign); err != nil {
			return err
		}
	}
	var dist [4]int
	for _, key := range dim.Cells("x", "y", "z") {
		dist[key.Level()]++
	}
	if dist != [4]int{1, 3, 3, 1} {
		return fmt.Errorf("api: level distribution %v", dist)
	}
	for _, cell := range c.View() {
		if Level(cell) != cell.Key.Level() {
			return fmt.Errorf("api: level mismatch")
		}
	}
	snap := viewMap(c.View())
	f := Fact{"q", "w", "r", 11}
	if err := apply(f, 1); err != nil { // apply re-checks cube==model
		return err
	} // each step; after -1 the model is back at snap.
	if err := apply(f, -1); err != nil {
		return err
	}
	if !reflect.DeepEqual(model, snap) { // belt and braces
		return fmt.Errorf("api: round trip changed state")
	}
	// Invariant 4: three distinct sentinels; a rejected op leaves no trace.
	small, _ := New(8)                    // 8 > 0: infallible
	_ = small.Add(Fact{"a", "b", "c", 1}) // first fact fills 8 == cap: infallible
	snap2 := viewMap(small.View())
	_, eBad := New(0)
	okRej := errors.Is(small.Add(Fact{"p", "q", "r", 1}), ErrCellLimit) &&
		errors.Is(small.Remove(Fact{"p", "q", "r", 1}), ErrFactNotFound) &&
		errors.Is(eBad, ErrInvalidMaxCells) && reflect.DeepEqual(viewMap(small.View()), snap2)
	if !okRej {
		return fmt.Errorf("api: rejection checks failed")
	}
	return nil
}
