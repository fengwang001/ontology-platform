package ontology

import (
	"math/rand/v2"
)

type treeNode struct {
	entry    *bufferEntry
	left     *treeNode
	right    *treeNode
	priority uint64
	size     int
}

func nodeSize(node *treeNode) int {
	if node == nil {
		return 0
	}
	return node.size
}

func maintain(node *treeNode) *treeNode {
	node.size = nodeSize(node.left) + 1 + nodeSize(node.right)
	return node
}

func compareEntry(a *bufferEntry, end int64, key string) int {
	if a.end < end {
		return -1
	}
	if a.end > end {
		return 1
	}
	if entryKeyString(a) < key {
		return -1
	}
	if entryKeyString(a) > key {
		return 1
	}
	return 0
}

func entryKeyString(entry *bufferEntry) string {
	return string(entry.key)
}

func mergeNodes(left, right *treeNode) *treeNode {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if left.priority > right.priority {
		left.right = mergeNodes(left.right, right)
		return maintain(left)
	}
	right.left = mergeNodes(left, right.left)
	return maintain(right)
}

func splitByEnd(node *treeNode, maxEnd int64) (*treeNode, *treeNode) {
	if node == nil {
		return nil, nil
	}
	if node.entry.end <= maxEnd {
		left, right := splitByEnd(node.right, maxEnd)
		node.right = left
		return maintain(node), right
	}
	left, right := splitByEnd(node.left, maxEnd)
	node.left = right
	return left, maintain(node)
}

func splitByRank(node *treeNode, count int) (*treeNode, *treeNode) {
	if node == nil {
		return nil, nil
	}
	leftSize := nodeSize(node.left)
	if count <= leftSize {
		left, right := splitByRank(node.left, count)
		node.left = right
		return left, maintain(node)
	}
	left, right := splitByRank(node.right, count-leftSize-1)
	node.right = left
	return maintain(node), right
}

func insertNode(root, inserted *treeNode) *treeNode {
	if root == nil {
		return maintain(inserted)
	}
	compared := compareEntry(inserted.entry, root.entry.end, entryKeyString(root.entry))
	if inserted.priority > root.priority {
		left, right := splitInsert(root, inserted)
		inserted.left = left
		inserted.right = right
		return maintain(inserted)
	}
	if compared < 0 {
		root.left = insertNode(root.left, inserted)
	} else {
		root.right = insertNode(root.right, inserted)
	}
	return maintain(root)
}

func splitInsert(root, inserted *treeNode) (*treeNode, *treeNode) {
	if root == nil {
		return nil, nil
	}
	compared := compareEntry(root.entry, inserted.entry.end, entryKeyString(inserted.entry))
	if compared < 0 {
		left, right := splitInsert(root.right, inserted)
		root.right = left
		return maintain(root), right
	}
	left, right := splitInsert(root.left, inserted)
	root.left = right
	return left, maintain(root)
}

func rankOfEntry(node *treeNode, end int64, key string) int {
	if node == nil {
		return 0
	}
	compared := compareEntry(node.entry, end, key)
	if compared == 0 {
		return nodeSize(node.left) + 1
	}
	if compared < 0 {
		return nodeSize(node.left) + 1 + rankOfEntry(node.right, end, key)
	}
	return rankOfEntry(node.left, end, key)
}

func appendEntriesInOrder(node *treeNode, entries []Emitted, kind EmitKind) []Emitted {
	if node == nil {
		return entries
	}
	entries = appendEntriesInOrder(node.left, entries, kind)
	entries = append(entries, Emitted{
		Key:    append([]byte(nil), node.entry.key...),
		WS:     node.entry.ws,
		End:    node.entry.end,
		Value:  node.entry.value,
		LastTS: node.entry.lastTS,
		Kind:   kind,
	})
	return appendEntriesInOrder(node.right, entries, kind)
}

func appendBufferedInOrder(node *treeNode, entries []BufferedEntry) []BufferedEntry {
	if node == nil {
		return entries
	}
	entries = appendBufferedInOrder(node.left, entries)
	entries = append(entries, BufferedEntry{
		Key:    append([]byte(nil), node.entry.key...),
		WS:     node.entry.ws,
		End:    node.entry.end,
		Value:  node.entry.value,
		LastTS: node.entry.lastTS,
	})
	return appendBufferedInOrder(node.right, entries)
}

func appendKeysInOrder(node *treeNode, keys map[entryKey]struct{}) map[entryKey]struct{} {
	if node == nil {
		return keys
	}
	keys = appendKeysInOrder(node.left, keys)
	keys[entryKey{key: string(node.entry.key), ws: node.entry.ws}] = struct{}{}
	return appendKeysInOrder(node.right, keys)
}

func newTreeNode(entry *bufferEntry) *treeNode {
	return &treeNode{
		entry:    entry,
		priority: rand.Uint64(),
		size:     1,
	}
}
