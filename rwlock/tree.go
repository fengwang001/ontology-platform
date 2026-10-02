package rwlock

// child is one live sequential ephemeral node of a lock.
type child struct {
	seq     int64
	kind    Kind
	session int64
	zxid    int64 // creation zxid
	held    bool
	watch   int64 // sequence of the child this waiter watches; -1 when held
	ws      int64 // watch registration sequence; 0 when held
}

// avlnode is one node of the AVL tree keyed by child sequence.
type avlnode struct {
	c      *child
	left   *avlnode
	right  *avlnode
	height int
	size   int
	maxW   int64 // largest W sequence in subtree, -1 if none
}

func (t *avlnode) h() int {
	if t == nil {
		return 0
	}
	return t.height
}

func (t *avlnode) sz() int {
	if t == nil {
		return 0
	}
	return t.size
}

func (t *avlnode) mw() int64 {
	if t == nil {
		return -1
	}
	return t.maxW
}

func (t *avlnode) pull() {
	t.height = 1 + max(t.left.h(), t.right.h())
	t.size = 1 + t.left.sz() + t.right.sz()
	t.maxW = max(t.left.mw(), t.right.mw())
	if t.c.kind == Write {
		if t.c.seq > t.maxW {
			t.maxW = t.c.seq
		}
	}
}

func rotateRight(t *avlnode) *avlnode {
	l := t.left
	t.left = l.right
	l.right = t
	t.pull()
	l.pull()
	return l
}

func rotateLeft(t *avlnode) *avlnode {
	r := t.right
	t.right = r.left
	r.left = t
	t.pull()
	r.pull()
	return r
}

func balance(t *avlnode) *avlnode {
	t.pull()
	if t.left.h()-t.right.h() > 1 {
		if t.left.left.h() < t.left.right.h() {
			t.left = rotateLeft(t.left)
		}
		return rotateRight(t)
	}
	if t.right.h()-t.left.h() > 1 {
		if t.right.right.h() < t.right.left.h() {
			t.right = rotateRight(t.right)
		}
		return rotateLeft(t)
	}
	return t
}

// tree is an AVL set of children keyed by sequence, counting tree-pointer
// probes so evaluation costs can be verified deterministically.
type tree struct {
	root   *avlnode
	probes int
}

func (tr *tree) touch(t *avlnode) *avlnode {
	tr.probes++
	return t
}

func (tr *tree) size() int { return tr.root.sz() }

func (tr *tree) insert(t *avlnode, c *child) *avlnode {
	if t == nil {
		return tr.touch(&avlnode{c: c, height: 1, size: 1, maxW: -1})
	}
	tr.touch(t)
	if c.seq < t.c.seq {
		t.left = tr.insert(t.left, c)
	} else {
		t.right = tr.insert(t.right, c)
	}
	return balance(t)
}

func (tr *tree) remove(t *avlnode, seq int64) *avlnode {
	if t == nil {
		return nil
	}
	tr.touch(t)
	if seq < t.c.seq {
		t.left = tr.remove(t.left, seq)
	} else if seq > t.c.seq {
		t.right = tr.remove(t.right, seq)
	} else {
		if t.left == nil || t.right == nil {
			if t.left != nil {
				return t.left
			}
			return t.right
		}
		s := t.right
		for s.left != nil {
			tr.touch(s)
			s = s.left
		}
		t.c = s.c
		t.right = tr.remove(t.right, s.c.seq)
	}
	return balance(t)
}

// find returns the child with seq, or nil.
func (tr *tree) find(t *avlnode, seq int64) *child {
	for t != nil {
		tr.touch(t)
		switch {
		case seq < t.c.seq:
			t = t.left
		case seq > t.c.seq:
			t = t.right
		default:
			return t.c
		}
	}
	return nil
}

// predecessor returns the child with the largest sequence below seq, or nil.
func (tr *tree) predecessor(t *avlnode, seq int64) *child {
	var best *child
	for t != nil {
		tr.touch(t)
		if seq <= t.c.seq {
			t = t.left
		} else {
			best = t.c
			t = t.right
		}
	}
	return best
}

// maxWBelow returns the largest W sequence strictly below seq, or -1.
func (tr *tree) maxWBelow(t *avlnode, seq int64) int64 {
	best := int64(-1)
	for t != nil {
		tr.touch(t)
		if seq <= t.c.seq {
			t = t.left
			continue
		}
		if t.left.mw() >= 0 {
			best = t.left.mw()
		}
		if t.c.kind == Write && t.c.seq > best {
			best = t.c.seq
		}
		t = t.right
	}
	return best
}

// minSeq returns the smallest sequence in the tree, or -1 when empty.
func (tr *tree) minSeq(t *avlnode) int64 {
	if t == nil {
		return -1
	}
	for t.left != nil {
		tr.touch(t)
		t = t.left
	}
	tr.touch(t)
	return t.c.seq
}

// inorder appends all children by ascending sequence to dst.
func (tr *tree) inorder(t *avlnode, dst []*child) []*child {
	if t == nil {
		return dst
	}
	dst = tr.inorder(t.left, dst)
	dst = append(dst, t.c)
	dst = tr.inorder(t.right, dst)
	return dst
}
