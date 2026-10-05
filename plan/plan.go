// Package plan computes next-hop versions for a firmware OTA campaign.
//
// A plan is defined by a target version T and a set of mandatory
// versions M (all distinct, positive and less than T). The next hop of a
// device at version v is the smallest mandatory version strictly greater
// than v, or T when no such version exists. A device already sitting
// exactly on a mandatory version never revisits it.
package plan

import (
	"errors"
	"sort"
)

// ErrInvalid reports an out-of-range target or mandatory version set.
var ErrInvalid = errors.New("plan: invalid target or mandatory versions")

const (
	minTarget = 2
	maxTarget = 1_000_000
	maxHops   = 64
)

// Plan is an immutable next-hop table for one campaign.
type Plan struct {
	target int
	hops   []int // mandatory versions, sorted ascending
}

// New validates T and M and returns the plan. T must be in
// [2, 10^6]; M must hold at most 64 distinct positive versions, each < T.
func New(target int, mandatory []int) (*Plan, error) {
	if target < minTarget || target > maxTarget {
		return nil, ErrInvalid
	}
	if len(mandatory) > maxHops {
		return nil, ErrInvalid
	}
	seen := make(map[int]struct{}, len(mandatory))
	hops := make([]int, 0, len(mandatory))
	for _, m := range mandatory {
		if m < 1 || m >= target {
			return nil, ErrInvalid
		}
		if _, dup := seen[m]; dup {
			return nil, ErrInvalid
		}
		seen[m] = struct{}{}
		hops = append(hops, m)
	}
	sort.Ints(hops)
	return &Plan{target: target, hops: hops}, nil
}

// Target returns the campaign target version T.
func (p *Plan) Target() int { return p.target }

// Next returns the next hop for a device at version v: the smallest
// mandatory version strictly greater than v, or T when none remains.
func (p *Plan) Next(v int) int {
	i := sort.SearchInts(p.hops, v+1)
	if i < len(p.hops) {
		return p.hops[i]
	}
	return p.target
}
