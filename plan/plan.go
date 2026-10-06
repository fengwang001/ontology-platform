// Package plan computes next-hop versions for devices moving toward a
// target firmware version through a set of mandatory versions.
package plan

import (
	"errors"
	"sort"
)

// ErrInvalid reports a target version or mandatory set outside the
// allowed envelope.
var ErrInvalid = errors.New("plan: invalid argument")

const (
	minTarget = 2
	maxTarget = 1_000_000
	maxMust   = 64
)

// Plan is an immutable next-hop table for one campaign.
type Plan struct {
	target int
	must   []int // sorted ascending, distinct, each in [1, target)
}

// New validates the target version T and the mandatory version set M and
// returns the plan. M must hold 0..64 distinct positive versions below T.
func New(target int, must []int) (*Plan, error) {
	if target < minTarget || target > maxTarget {
		return nil, ErrInvalid
	}
	if len(must) > maxMust {
		return nil, ErrInvalid
	}
	sorted := make([]int, len(must))
	copy(sorted, must)
	sort.Ints(sorted)
	for i, m := range sorted {
		if m < 1 || m >= target {
			return nil, ErrInvalid
		}
		if i > 0 && sorted[i-1] == m {
			return nil, ErrInvalid
		}
	}
	return &Plan{target: target, must: sorted}, nil
}

// Target returns the campaign target version T.
func (p *Plan) Target() int { return p.target }

// Next returns the next hop for a device at version v: the smallest
// mandatory version strictly greater than v, or T when none remains.
// A device exactly on a mandatory version does not repeat it.
func (p *Plan) Next(v int) int {
	i := sort.Search(len(p.must), func(i int) bool { return p.must[i] > v })
	if i < len(p.must) {
		return p.must[i]
	}
	return p.target
}
