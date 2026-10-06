package waitlist

const noListEntry int64 = -1

type listNode struct {
	prev int64
	next int64
}

type waitingList struct {
	head    int64
	tail    int64
	nodes   map[int64]*listNode
	entries map[int64]*Entry
}

func newWaitingList(entries map[int64]*Entry) *waitingList {
	return &waitingList{head: noListEntry, tail: noListEntry, nodes: make(map[int64]*listNode), entries: entries}
}

func (l *waitingList) appendEntry(id int64) {
	node := &listNode{prev: l.tail, next: noListEntry}
	l.nodes[id] = node
	if l.tail != noListEntry {
		l.nodes[l.tail].next = id
	} else {
		l.head = id
	}
	l.tail = id
}

func (l *waitingList) insertOrdered(id int64, registeredAt int64) {
	node := &listNode{prev: noListEntry, next: noListEntry}
	l.nodes[id] = node
	anchor := l.head
	for anchor != noListEntry {
		current := l.entries[anchor]
		if current.RegisteredAt > registeredAt || current.RegisteredAt == registeredAt && current.ID > id {
			break
		}
		anchor = l.nodes[anchor].next
	}
	if anchor == noListEntry {
		node.prev = l.tail
		if l.tail != noListEntry {
			l.nodes[l.tail].next = id
		} else {
			l.head = id
		}
		l.tail = id
		return
	}
	previous := l.nodes[anchor].prev
	node.prev = previous
	node.next = anchor
	l.nodes[anchor].prev = id
	if previous == noListEntry {
		l.head = id
	} else {
		l.nodes[previous].next = id
	}
}

func (l *waitingList) remove(id int64) {
	node := l.nodes[id]
	if node.prev != noListEntry {
		l.nodes[node.prev].next = node.next
	} else {
		l.head = node.next
	}
	if node.next != noListEntry {
		l.nodes[node.next].prev = node.prev
	} else {
		l.tail = node.prev
	}
	delete(l.nodes, id)
}

func (l *waitingList) scan(cutoff int64, visit func(*Entry) bool) {
	id := l.head
	for id != noListEntry {
		node := l.nodes[id]
		entry := l.entries[id]
		next := node.next
		if entry.RegisteredAt > cutoff || visit(entry) {
			return
		}
		id = next
	}
}

func (l *waitingList) ordered() []int64 {
	var ids []int64
	for id := l.head; id != noListEntry; id = l.nodes[id].next {
		ids = append(ids, id)
	}
	return ids
}

type waitingLists struct{ lists [3]*waitingList }

func newWaitingLists(entries map[int64]*Entry) *waitingLists {
	return &waitingLists{lists: [3]*waitingList{newWaitingList(entries), newWaitingList(entries), newWaitingList(entries)}}
}

func (l *waitingLists) appendEntry(id int64, priority Priority) {
	l.lists[priority].appendEntry(id)
}

func (l *waitingLists) insertOrdered(id int64, registeredAt int64, priority Priority) {
	l.lists[priority].insertOrdered(id, registeredAt)
}

func (l *waitingLists) remove(id int64, priority Priority) { l.lists[priority].remove(id) }

func (l *waitingLists) scan(cutoff int64, priority Priority, visit func(*Entry) bool) {
	l.lists[priority].scan(cutoff, visit)
}

func (l *waitingLists) ordered(priority Priority) []int64 { return l.lists[priority].ordered() }
