// Package query 实现布尔 AND/OR/NOT 与短语查询及相关性排序。
package query

import (
	"errors"

	"ontology/index"
)

var (
	// ErrEmptyQuery 空查询串。
	ErrEmptyQuery = errors.New("query: empty query")
	// ErrInvalidQuery 语法错误。
	ErrInvalidQuery = errors.New("query: invalid query")
)

// Result 是一条命中文档。
type Result struct {
	DocID uint64
	Score int
}

// Execute 在索引的一致快照上执行查询；结果按 score 降序、docID 升序排序。
func Execute(idx *index.Index, q string) ([]Result, error) {
	return nil, nil
}
