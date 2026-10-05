// Package plan computes the next-hop version for a device given the
// mandatory versions and the target version of an OTA campaign.
package plan

import (
	"errors"
	"sort"
)

// ErrInvalid reports invalid plan parameters.
var ErrInvalid = errors.New("plan: invalid parameters")

const (
	minTarget = 2
	maxTarget = 1_000_000
	maxMand   = 64
)

// Plan is an immutable next-hop table.
type Plan struct {
	target int
	mand   []int // sorted ascending, distinct, each in [1, target)
}

// New validates the target T and the mandatory version set M and
// returns the plan. M may be unordered but must contain distinct
// positive versions strictly below T.
func New(target int, mandatory []int) (*Plan, error) {
	if target < minTarget || target > maxTarget {
		return nil, ErrInvalid
	}
	if len(mandatory) > maxMand {
		return nil, ErrInvalid
	}
	m := make([]int, len(mandatory))
	copy(m, mandatory)
	sort.Ints(m)
	for i, v := range m {
		if v < 1 || v >= target {
			return nil, ErrInvalid
		}
		if i > 0 && v == m[i-1] {
			return nil, ErrInvalid
		}
	}
	return &Plan{target: target, mand: m}, nil
}

// Target returns the target version T.
func (p *Plan) Target() int { return p.target }

// Next returns the smallest mandatory version strictly greater than v,
// or the target version when no such mandatory version exists. A v
// exactly equal to a mandatory version is not repeated.
func (p *Plan) Next(v int) int {
	i := sort.SearchInts(p.mand, v+1)
	if i < len(p.mand) {
		return p.mand[i]
	}
	return p.target
}
