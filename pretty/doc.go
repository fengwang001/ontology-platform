// Package pretty implements a line-width adaptive layout engine (a
// Wadler/Prettier-style pretty printer). Callers build an immutable document
// tree, register named fragments on a Session, and render the tree to text
// for a given line width. Structures that fit on one line are flattened;
// structures that do not fit are broken at their breakable points with
// indentation.
package pretty

// Doc is an immutable document tree node. Instances are safe to share and
// render concurrently; rendering never mutates a Doc.
type Doc interface{ isDoc() }

type textNode struct{ s string } // literal text, no newline allowed
type spaceNode struct{}          // breakable space: " " flat, newline broken
type softNode struct{}           // breakable empty: "" flat, newline broken
type hardNode struct{}           // forced newline
type indentNode struct {
	n    int
	body Doc
}                                           // add n columns of indentation to body
type alignNode struct{ body Doc }           // set indentation to the current column
type groupNode struct{ body Doc }           // layout decision unit: flat or broken
type condNode struct{ broken, flat string } // text chosen by enclosing group mode
type seqNode struct{ parts []Doc }          // concatenation
type refNode struct{ name string }          // reference to a registered fragment

func (*textNode) isDoc()   {}
func (*spaceNode) isDoc()  {}
func (*softNode) isDoc()   {}
func (*hardNode) isDoc()   {}
func (*indentNode) isDoc() {}
func (*alignNode) isDoc()  {}
func (*groupNode) isDoc()  {}
func (*condNode) isDoc()   {}
func (*seqNode) isDoc()    {}
func (*refNode) isDoc()    {}

var (
	sharedSpace = &spaceNode{}
	sharedSoft  = &softNode{}
	sharedHard  = &hardNode{}
)

// Text returns a literal text node. The text must not contain '\n'.
func Text(s string) Doc { return &textNode{s: s} }

// BreakableSpace renders as one space when flat, as a newline when broken.
func BreakableSpace() Doc { return sharedSpace }

// BreakableEmpty renders as nothing when flat, as a newline when broken.
func BreakableEmpty() Doc { return sharedSoft }

// HardLine always renders a newline and forces every enclosing group to break.
func HardLine() Doc { return sharedHard }

// Indent adds n columns of indentation to body. n must be non-negative.
func Indent(n int, body Doc) Doc { return &indentNode{n: n, body: body} }

// Align resets the indentation of body to the column at which the node is
// entered; nested Indent nodes accumulate on top of that column.
func Align(body Doc) Doc { return &alignNode{body: body} }

// Group makes body a layout decision unit: it is rendered flat if it (plus
// the following content up to the first broken breakable) fits in the
// remaining line width, and broken otherwise.
func Group(body Doc) Doc { return &groupNode{body: body} }

// Cond outputs broken when the nearest enclosing group is broken and flat
// when it is flat. Outside any group it counts as broken. Neither variant
// may contain '\n'.
func Cond(broken, flat string) Doc { return &condNode{broken: broken, flat: flat} }

// Seq concatenates parts in order.
func Seq(parts ...Doc) Doc { return &seqNode{parts: parts} }

// Ref references a fragment registered on the Session by name. The fragment
// is expanded in place at render time.
func Ref(name string) Doc { return &refNode{name: name} }
