package ontology

import "errors"

var (
	// ErrEmptyRing 表示在空环上执行 Locate。
	ErrEmptyRing = errors.New("ontology: empty ring")
	// ErrInvalidVnodes 表示添加节点时 vnodes <= 0。
	ErrInvalidVnodes = errors.New("ontology: vnodes must be positive")
	// ErrNodeExists 表示重复添加同一节点 ID。
	ErrNodeExists = errors.New("ontology: node already exists")
	// ErrNodeNotFound 表示删除不存在的节点。
	ErrNodeNotFound = errors.New("ontology: node not found")
)
