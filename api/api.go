// Package api exposes the incremental 3-way natural-join materialized view.
package api

import (
	"ontology/join"
	"ontology/rel"
)

type (
	Tuple = rel.Tuple
	Quad  = join.Quad
)

// Three distinct decidable sentinel errors.
var (
	ErrBadTable = join.ErrBadTable
	ErrNotFound = join.ErrNotFound
	ErrLimit    = join.ErrLimit
)

type View struct{ db *join.DB }

func New(maxResults int) *View                   { return &View{db: join.New(maxResults)} }
func (v *View) Insert(tab string, t Tuple) error { return v.db.Insert(tab, t) }
func (v *View) Delete(tab string, t Tuple) error { return v.db.Delete(tab, t) }
func (v *View) Result() map[Quad]int             { return v.db.Result() }
func (v *View) SelfCheck() error                 { return v.db.SelfCheck() }
