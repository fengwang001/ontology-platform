package kerberos

type replayKey struct {
	ticket   string
	authTime int64
}

type replayNode struct {
	key     replayKey
	expires int64
	child   *replayNode
	next    *replayNode
	prev    *replayNode
}

type replayCache struct {
	entries map[replayKey]*replayNode
	root    *replayNode
	pops    int64
}

func newReplayCache() replayCache {
	return replayCache{entries: make(map[replayKey]*replayNode)}
}

func (c *replayCache) add(key replayKey, expires int64) {
	node := &replayNode{key: key, expires: expires}
	c.entries[key] = node
	c.root = meldReplay(c.root, node)
}

func (c *replayCache) cleanup(now int64) {
	for c.root != nil && c.root.expires < now {
		old := c.root
		c.root = mergeReplayChildren(old.child)
		old.child = nil
		old.next = nil
		old.prev = nil
		delete(c.entries, old.key)
		c.pops++
	}
}

func meldReplay(a, b *replayNode) *replayNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.expires > b.expires {
		a, b = b, a
	}
	b.prev = a
	b.next = a.child
	if a.child != nil {
		a.child.prev = b
	}
	a.child = b
	return a
}

func mergeReplayChildren(first *replayNode) *replayNode {
	if first == nil {
		return nil
	}
	if first.prev != nil {
		first.prev.next = nil
		first.prev = nil
	}

	var pairs []*replayNode
	for first != nil {
		a := first
		b := first.next
		if b == nil {
			a.prev = nil
			a.next = nil
			pairs = append(pairs, a)
			break
		}
		rest := b.next
		a.prev = nil
		a.next = nil
		b.prev = nil
		b.next = nil
		if rest != nil {
			rest.prev = nil
		}
		pairs = append(pairs, meldReplay(a, b))
		first = rest
	}

	result := pairs[len(pairs)-1]
	for i := len(pairs) - 2; i >= 0; i-- {
		result = meldReplay(pairs[i], result)
	}
	return result
}
