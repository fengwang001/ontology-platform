package hygiene

type source int

const (
	sourceUser source = iota
	sourceGlobal
	sourceBound
	sourceLiteral
)

type node struct {
	atom     bool
	text     string
	items    []*node
	depth    int
	source   source
	binding  *binding
	isBinder bool
}

type binding struct {
	final      int
	introduced bool
}

type macro struct {
	name     string
	params   []string
	template *node
}
