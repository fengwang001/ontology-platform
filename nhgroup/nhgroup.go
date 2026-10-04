package nhgroup

import (
	"errors"
	"sort"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRollback   = errors.New("clock rollback")
	ErrNotFound        = errors.New("not found")
	ErrAlreadyExists   = errors.New("already exists")
	ErrLastMember      = errors.New("last member")
	ErrInUse           = errors.New("in use")
	ErrNoRoute         = errors.New("no route")
	ErrNoNexthop       = errors.New("no nexthop")
)

type Member struct {
	Nexthop uint32
	Weight  uint32
	Alive   bool
}

type Targets struct {
	Buckets map[uint32]int
	Alive   map[uint32]bool
	Total   int
}

func ValidateMember(nexthop, weight uint32) error {
	if nexthop == 0 || nexthop > 1_000_000 || weight == 0 || weight > 1000 {
		return ErrInvalidArgument
	}
	return nil
}

func TargetBuckets(bucketCount int, members []Member) Targets {
	targets := Targets{
		Buckets: make(map[uint32]int, len(members)),
		Alive:   make(map[uint32]bool, len(members)),
	}
	var totalWeight uint64
	for _, member := range members {
		if member.Alive {
			totalWeight += uint64(member.Weight)
			targets.Alive[member.Nexthop] = true
		}
	}
	if totalWeight == 0 {
		return targets
	}
	type remainder struct {
		nexthop uint32
		value   uint64
	}
	remainders := make([]remainder, 0, len(members))
	assigned := 0
	for _, member := range members {
		if !member.Alive {
			continue
		}
		scaled := uint64(bucketCount) * uint64(member.Weight)
		base := int(scaled / totalWeight)
		targets.Buckets[member.Nexthop] = base
		assigned += base
		remainders = append(remainders, remainder{member.Nexthop, scaled % totalWeight})
	}
	sort.Slice(remainders, func(i, j int) bool {
		if remainders[i].value != remainders[j].value {
			return remainders[i].value > remainders[j].value
		}
		return remainders[i].nexthop < remainders[j].nexthop
	})
	for idx := 0; assigned < bucketCount && idx < len(remainders); idx++ {
		targets.Buckets[remainders[idx].nexthop]++
		assigned++
	}
	targets.Total = bucketCount
	return targets
}

func Balance(bucketCount int, current map[uint32]int, targets Targets, aliveCount int) bool {
	if aliveCount == 0 {
		return true
	}
	empty := bucketCount
	for nh, count := range current {
		if targets.Alive[nh] {
			if count-targets.Buckets[nh] > 0 {
				return false
			}
			empty -= count
		}
	}
	return empty == 0
}
