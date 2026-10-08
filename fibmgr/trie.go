package fibmgr

// Color ids used in candidate sets: 0 = no route, 1 = blackhole,
// >= 2 = interned nexthop strings.
const (
	colorNone      uint32 = 0
	colorBlackhole uint32 = 1
)

// node is one node of the binary prefix trie. A node exists only where
// needed: on paths to control plane routes, or to hold a data plane
// entry whose prefix has no routes below it.
type node struct {
	child [2]*node

	// control plane
	hasRoute bool
	route    NextHop
	e        uint32 // effective control color inherited by gaps below

	// aggregation summary: representing this subtree costs a entries
	// when the inherited data plane color is in cs, a+1 otherwise.
	a  int
	cs []uint32

	// data plane solution
	solved   bool
	inh      uint32 // inherited color this solution was computed for
	hasEntry bool
	entry    uint32 // color of the entry at this node

	dirty    bool // summary changed since entries were last solved
	dirtySub bool // some descendant is dirty
}

func bit(addr uint32, d int) uint32 { return (addr >> uint(31-d)) & 1 }

// combine merges two child summaries into the parent's summary.
func combine(al int, cl []uint32, ar int, cr []uint32) (int, []uint32) {
	stats.ColorOps += int64(len(cl) + len(cr))
	if inter := intersectSorted(cl, cr); len(inter) > 0 {
		return al + ar, inter
	}
	return al + ar + 1, unionSorted(cl, cr)
}

func intersectSorted(a, b []uint32) []uint32 {
	var out []uint32
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

func unionSorted(a, b []uint32) []uint32 {
	out := make([]uint32, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		default:
			out = append(out, b[j])
			j++
		}
	}
	return append(append(out, a[i:]...), b[j:]...)
}

func containsCS(s []uint32, c uint32) bool {
	lo, hi := 0, len(s)
	for lo < hi {
		mid := (lo + hi) / 2
		switch {
		case s[mid] == c:
			return true
		case s[mid] < c:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return false
}

func equalCS(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
