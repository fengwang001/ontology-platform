// Package forward computes multicast forwarding port sets and
// classifies multicast group addresses. It is a pure-function package:
// all liveness decisions are made by the caller and passed in as
// sorted port lists.
package forward

const (
	minGroup = 0xE0000000
	maxGroup = 0xEFFFFFFF
	maxLocal = 0xE00000FF
)

// Valid reports whether group is a multicast group address.
func Valid(group uint32) bool {
	return group >= minGroup && group <= maxGroup
}

// LocalLink reports whether group is a link-local multicast group
// (224.0.0.x), which is always flooded.
func LocalLink(group uint32) bool {
	return group >= minGroup && group <= maxLocal
}

// Compute returns the sorted egress port set.
//
//	floodAll:            every port except inPort
//	len(members) > 0:    union of members and routers, minus inPort
//	otherwise:           routers minus inPort
//
// members and routers must be sorted ascending and contain no duplicates.
func Compute(ports, inPort int, floodAll bool, members, routers []int) []int {
	if floodAll {
		var out []int
		for p := 1; p <= ports; p++ {
			if p != inPort {
				out = append(out, p)
			}
		}
		return out
	}
	if len(members) == 0 {
		return exclude(routers, inPort)
	}
	out := make([]int, 0, len(members)+len(routers))
	i, j := 0, 0
	for i < len(members) || j < len(routers) {
		var p int
		switch {
		case j >= len(routers) || (i < len(members) && members[i] < routers[j]):
			p = members[i]
			i++
		case i >= len(members) || routers[j] < members[i]:
			p = routers[j]
			j++
		default:
			p = members[i]
			i++
			j++
		}
		if p != inPort {
			out = append(out, p)
		}
	}
	return out
}

func exclude(ports []int, inPort int) []int {
	var out []int
	for _, p := range ports {
		if p != inPort {
			out = append(out, p)
		}
	}
	return out
}
