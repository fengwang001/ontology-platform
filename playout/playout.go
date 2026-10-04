// Package playout answers what a linear channel broadcasts at any instant.
package playout

import "ontology/internal/timeline"

// Channel is a shared linear-channel timeline.
type Channel = timeline.Timeline

// Kind classifies the content playing at an instant.
type Kind = timeline.Kind

const (
	KindFiller   = timeline.KindFiller
	KindProgram  = timeline.KindProgram
	KindOverride = timeline.KindOverride
)

// Result is the playout decision at one instant.
type Result = timeline.Result

// At reports the content and in-content offset broadcast at time t.
func At(c *Channel, t int64) (Result, error) { return c.At(t) }

// ErrBadTime indicates t is outside the legal [0, 10^12] range.
var ErrBadTime = timeline.ErrBadTime
