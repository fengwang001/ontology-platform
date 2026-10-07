package docsync

import (
	"strings"
	"unicode/utf8"
)

// ropeNode is one physical line WITHOUT its trailing U+000A, except the last
// line which also has no feed. Aggregate size counts UTF-16 code units of all
// lines plus one feed per non-final line. Since the node boundaries are always
// exactly line boundaries, coordinate conversion never has to reason about a
// line being split across nodes: it locates the node (by line) or the offset
// (by cumulative size), both O(log n), and works within a single line string.
type ropeNode struct {
	line     string // line content, no '\n'
	priority uint64
	left     *ropeNode
	right    *ropeNode
	size     int // subtree UTF-16 units incl. feeds between lines
	lines    int // subtree node count
}

type rope struct {
	root    *ropeNode
	counter func()
}

func newRope(text string, counter func()) *rope {
	r := &rope{counter: counter}
	parts := strings.Split(text, "\n")
	nodes := make([]*ropeNode, len(parts))
	for i, ln := range parts {
		nodes[i] = r.newNode(ln, i < len(parts)-1)
	}
	var root *ropeNode
	for _, n := range nodes {
		root = r.merge(root, n)
	}
	r.root = root
	return r
}

// ownSize is a node's UTF-16 size: line units plus one feed unless it is the
// document's final line. Whether a node is final is contextual; we instead give
// every node's size a virtual trailing feed and keep total = sum(line)+(nodes-1)
// by having the final node contribute no feed. Final-ness is tracked via a
// subtree flag below.

func (r *rope) newNode(line string, hasFeed bool) *ropeNode {
	n := &ropeNode{line: line, priority: nextPriority()}
	n.size = utf16Len(line) + 1
	n.lines = 1
	return n
}

// pull treats each node as contributing lineLen + 1 (its feed), including the
// final node, which carries a harmless phantom trailing feed. Thus every node's
// span is uniform (lineLen+1) and every line i starts at global offset
// i + sum(lineLen over earlier lines); length() hides the phantom feed.
func (n *ropeNode) pull() {
	n.size = utf16Len(n.line) + 1
	n.lines = 1
	if n.left != nil {
		n.size += n.left.size
		n.lines += n.left.lines
	}
	if n.right != nil {
		n.size += n.right.size
		n.lines += n.right.lines
	}
}

func (r *rope) touch(n *ropeNode) {
	if n != nil && r.counter != nil {
		r.counter()
	}
}

func (r *rope) merge(a, b *ropeNode) *ropeNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.priority > b.priority {
		a.right = r.merge(a.right, b)
		a.pull()
		return a
	}
	b.left = r.merge(a, b.left)
	b.pull()
	return b
}

// mergeNodes merges line nodes; every boundary between nodes is a feed.

func runeWidth16(size int) int {
	if size == 4 {
		return 2
	}
	return 1
}

func byteOffsetAt16(s string, u16 int) int {
	cur, byteOff := 0, 0
	for byteOff < len(s) {
		if cur == u16 {
			return byteOff
		}
		_, size := utf8.DecodeRuneInString(s[byteOff:])
		cur += runeWidth16(size)
		byteOff += size
	}
	return byteOff
}

// cutByOffset splits the rope at a valid UTF-16 boundary offset. It returns the
// full lines before and after plus the two fragments of the touched line.

// Build a treap from complete line strings (all but the last get a feed).
func (r *rope) buildLines(lines []string) *ropeNode {
	var root *ropeNode
	for i, ln := range lines {
		root = r.merge(root, r.newNode(ln, i < len(lines)-1))
	}
	return root
}

func (r *rope) length() int {
	if r.root == nil {
		return 0
	}
	return r.root.size - 1 // hide the phantom trailing feed of the last node
}

func (r *rope) lineCount() int {
	if r.root == nil {
		return 1
	}
	return r.root.lines
}

// positionOf maps a UTF-16 offset to (line, column). It indexes a subtree by
// the global offset of its first unit (subBaseOff) and its first line index
// (subBaseLine). Every node spans lineLen+1 units (line then feed).
func (r *rope) positionOf(off int) (p Position, split bool, ok bool) {
	if off < 0 || off > r.length() {
		return Position{}, false, false
	}
	t := r.root
	subBaseOff, subBaseLine := 0, 0
	for t != nil {
		r.touch(t)
		ls, ll := 0, 0
		if t.left != nil {
			ls, ll = t.left.size, t.left.lines
		}
		lineLen := utf16Len(t.line)
		nodeStart := subBaseOff + ls
		nodeLine := subBaseLine + ll
		switch {
		case off < nodeStart:
			t = t.left
		case off <= nodeStart+lineLen:
			col := off - nodeStart
			st, _ := classifyUTF16Offset(t.line, col)
			if st == 2 {
				return Position{}, true, false
			}
			return Position{Line: nodeLine, Character: col}, false, true
		default:
			// Feed or next line; descend right.
			subBaseOff = nodeStart + lineLen + 1
			subBaseLine = nodeLine + 1
			t = t.right
		}
	}
	return Position{}, false, false
}

// offsetOf maps (line,column) to a UTF-16 offset. It indexes a subtree by the
// number of lines before it (subBaseLine) and the global offset of the
// subtree's first unit (subBaseOff). Every node spans lineLen+1 units.
func (r *rope) offsetOf(p Position) (off int, split bool, ok bool) {
	if p.Line < 0 || p.Character < 0 || p.Line >= r.lineCount() {
		return 0, false, false
	}
	t := r.root
	subBaseOff, subBaseLine := 0, 0
	for t != nil {
		r.touch(t)
		ll := 0
		if t.left != nil {
			ll = t.left.lines
		}
		nodeLine := subBaseLine + ll
		switch {
		case p.Line < nodeLine:
			t = t.left // same subtree base
		case p.Line > nodeLine:
			// Descend into right subtree; its base skips left subtree, node
			// line and its feed.
			ls := 0
			if t.left != nil {
				ls = t.left.size
			}
			subBaseOff = subBaseOff + ls + utf16Len(t.line) + 1
			subBaseLine = nodeLine + 1
			t = t.right
		default:
			ls := 0
			if t.left != nil {
				ls = t.left.size
			}
			nodeStart := subBaseOff + ls
			lineLen := utf16Len(t.line)
			st, _ := classifyUTF16Offset(t.line, p.Character)
			if st == 2 {
				return 0, true, false
			}
			if p.Character > lineLen {
				return 0, false, false
			}
			return nodeStart + p.Character, false, true
		}
	}
	return 0, false, false
}

// mergeT merges two line treaps; all nodes keep their stored spans.
func (r *rope) mergeT(a, b *ropeNode) *ropeNode { return r.merge(a, b) }

// splitAtLine splits t into the first k lines and the rest, both treaps.
func (r *rope) splitAtLine(t *ropeNode, k int) (a, b *ropeNode) {
	if t == nil {
		return nil, nil
	}
	r.touch(t)
	leftLines := 0
	if t.left != nil {
		leftLines = t.left.lines
	}
	switch {
	case k <= leftLines:
		var aa *ropeNode
		aa, t.left = r.splitAtLine(t.left, k)
		t.pull()
		return aa, t
	case k > leftLines+1:
		var bb *ropeNode
		t.right, bb = r.splitAtLine(t.right, k-leftLines-1)
		t.pull()
		return t, bb
	default:
		// Cut between left subtree+node and right subtree.
		right := t.right
		t.right = nil
		t.pull()
		return t, right
	}
}

// nodeAtLine detaches and returns the node at line index k plus the treaps of
// lines before and after it.
func (r *rope) extractLine(t *ropeNode, k int) (before *ropeNode, node *ropeNode, after *ropeNode) {
	before, rest := r.splitAtLine(t, k)
	one, after := r.splitAtLine(rest, 1)
	return before, one, after
}

func (r *rope) replace(s, e int, text string) {
	sp, _, _ := r.positionOf(s)
	ep, _, _ := r.positionOf(e)
	midLines := strings.Split(text, "\n")

	if sp.Line == ep.Line {
		before, one, after := r.extractLine(r.root, sp.Line)
		line := one.line
		sb := byteOffsetAt16(line, sp.Character)
		eb := byteOffsetAt16(line, ep.Character)
		joined := line[:sb] + text + line[eb:]
		parts := strings.Split(joined, "\n")
		r.root = r.mergeT(r.mergeT(before, r.buildLines(parts)), after)
		return
	}
	// Spans multiple lines: isolate the [startLine, endLine] inclusive block.
	prefix, restBlock := r.splitAtLine(r.root, sp.Line)
	block, suffix := r.splitAtLine(restBlock, ep.Line-sp.Line+1)
	var blockLines []string
	r.collectLines(block, &blockLines)
	startLine := blockLines[0]
	endLine := blockLines[len(blockLines)-1]
	sb := byteOffsetAt16(startLine, sp.Character)
	eb := byteOffsetAt16(endLine, ep.Character)
	joined := startLine[:sb] + text + endLine[eb:]
	parts := strings.Split(joined, "\n")
	r.root = r.mergeT(r.mergeT(prefix, r.buildLines(parts)), suffix)
	_ = midLines
}

func (r *rope) collectLines(n *ropeNode, out *[]string) {
	if n == nil {
		return
	}
	r.collectLines(n.left, out)
	r.touch(n)
	*out = append(*out, n.line)
	r.collectLines(n.right, out)
}

func (r *rope) string() string {
	var lines []string
	r.collectLines(r.root, &lines)
	return strings.Join(lines, "\n")
}
