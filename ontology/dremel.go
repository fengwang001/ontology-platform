package ontology

import "sync"

// Shredder 是 Dremel 风格的嵌套记录列式拆分/重装器。
type Shredder struct {
	mu         sync.RWMutex
	schema     []Field
	indexTree  []schemaNode
	leaves     []leafInfo
	pageSize   int
	maxEntries int
	cols       []colState
	leafPaths  [][]*schemaNode
	recordN    int
	read       int
}

type schemaNode struct {
	field    *Field
	path     string
	children []schemaNode
	leafLo   int
	leafHi   int
	repLevel int
	defLevel int
}

type colState struct {
	entries  []Entry
	pages    []PageInfo
	curStart int
	curRecs  int
	curEntry int
	nulls    int
	presents int
	min      int64
	max      int64
	hasValue bool
}

// New 按模式构建 Shredder。
func New(schema []Field, pageEntries, maxEntries int) (*Shredder, error) {
	leaves, err := validateSchema(schema)
	if err != nil {
		return nil, err
	}
	if pageEntries < 1 || maxEntries < 1 {
		return nil, &FieldError{Kind: ErrParam, Path: ""}
	}
	tree := buildIndexTree(schema)
	return &Shredder{
		schema:     append([]Field(nil), schema...),
		indexTree:  tree,
		leaves:     leaves,
		leafPaths:  collectLeafPaths(tree),
		pageSize:   pageEntries,
		maxEntries: maxEntries,
		cols:       make([]colState, len(leaves)),
	}, nil
}

// LeafNames 按模式深度优先次序返回所有叶子列名。
func (s *Shredder) LeafNames() []string {
	names := make([]string, len(s.leaves))
	for i, l := range s.leaves {
		names[i] = l.name
	}
	return names
}
