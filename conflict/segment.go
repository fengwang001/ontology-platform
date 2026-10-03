package conflict

// segment is one element of the document sequence: either a run of body
// lines (blk == nil) or a single unresolved block.
type segment struct {
	lines []string
	blk   *block
}

// block is one unresolved diff3-style conflict block.
type block struct {
	startLabel string
	baseLabel  string
	sepLabel   string
	endLabel   string
	hasBase    bool
	ours       []string
	base       []string
	theirs     []string
}
