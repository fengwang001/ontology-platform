package traversal

import "strconv"

func (d Direction) String() string {
	switch d {
	case DirOutbound:
		return "out"
	case DirInbound:
		return "in"
	case DirBoth:
		return "both"
	default:
		return "invalid(" + strconv.Itoa(int(d)) + ")"
	}
}
