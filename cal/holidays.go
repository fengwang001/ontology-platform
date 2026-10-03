package cal

import "math/rand"

// holidaySet 是以 treap 实现的日号有序集合，
// 支持插入、存在性与 successor 查询。
type holidaySet struct {
	root *hnode
	rng  *rand.Rand
}

type hnode struct {
	day   int64
	left  *hnode
	right *hnode
	prio  uint32
	// cnt[w] 为子树内 day%7==w 的节点个数。
	cnt [7]int
}

func newHolidaySet() *holidaySet {
	return &holidaySet{rng: rand.New(rand.NewSource(1))}
}

func (n *hnode) pull() {
	var cnt [7]int
	for _, sub := range []*hnode{n.left, n.right} {
		if sub != nil {
			for w := 0; w < 7; w++ {
				cnt[w] += sub.cnt[w]
			}
		}
	}
	cnt[n.day%7]++
	n.cnt = cnt
}

func (s *holidaySet) rotateRight(n *hnode) *hnode {
	x := n.left
	n.left = x.right
	x.right = n
	n.pull()
	x.pull()
	return x
}

func (s *holidaySet) rotateLeft(n *hnode) *hnode {
	x := n.right
	n.right = x.left
	x.left = n
	n.pull()
	x.pull()
	return x
}

// insert 插入日号；已存在时为无操作。
func (s *holidaySet) insert(day int64) {
	s.root = s.insertAt(s.root, day)
}

func (s *holidaySet) insertAt(n *hnode, day int64) *hnode {
	if n == nil {
		node := &hnode{day: day, prio: s.rng.Uint32()}
		node.pull()
		return node
	}
	if day == n.day {
		return n
	}
	if day < n.day {
		n.left = s.insertAt(n.left, day)
		if n.left.prio < n.prio {
			n = s.rotateRight(n)
		}
	} else {
		n.right = s.insertAt(n.right, day)
		if n.right.prio < n.prio {
			n = s.rotateLeft(n)
		}
	}
	n.pull()
	return n
}

// contains 判断日号是否为节假日。
func (s *holidaySet) contains(day int64) bool {
	n := s.root
	for n != nil {
		switch {
		case day == n.day:
			return true
		case day < n.day:
			n = n.left
		default:
			n = n.right
		}
	}
	return false
}

// countWorkdayBelow 返回 < day 且星期位在 mask 中的节假日总数（O(log n)）。
func (s *holidaySet) countWorkdayBelow(day int64, mask uint8) int64 {
	var total int64
	n := s.root
	for n != nil {
		if day <= n.day {
			n = n.left
			continue
		}
		if n.left != nil {
			for w := uint(0); w < 7; w++ {
				if mask&(1<<w) != 0 {
					total += int64(n.left.cnt[w])
				}
			}
		}
		if mask&(1<<uint(n.day%7)) != 0 {
			total++
		}
		n = n.right
	}
	return total
}

// successor 返回 >= day 的最小节假日，第二个返回值表示是否存在。
func (s *holidaySet) successor(day int64) (int64, bool) {
	var best int64
	found := false
	n := s.root
	for n != nil {
		if day <= n.day {
			best, found = n.day, true
			n = n.left
		} else {
			n = n.right
		}
	}
	return best, found
}
