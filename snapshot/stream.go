package snapshot

import "errors"

// ErrEnd 由 Reader.Read 在记录流耗尽时返回。
var ErrEnd = errors.New("snapshot: end of record stream")

// Reader 逐条产出快照记录。
//
// Read 在流耗尽时返回 ErrEnd；校验器在定位到损坏记录后立即停止调用
// Read，因此实现方无需也不应预读后续记录。
type Reader interface {
	Read() (Record, error)
}

// SliceReader 基于内存切片实现 Reader，校验期间不会修改底层切片。
type SliceReader struct {
	records []Record
	pos     int
}

// NewSliceReader 基于给定记录切片构造 Reader。
func NewSliceReader(records []Record) *SliceReader {
	return &SliceReader{records: records}
}

// Read 返回下一条记录；流耗尽时返回 ErrEnd。
func (r *SliceReader) Read() (Record, error) {
	if r.pos >= len(r.records) {
		return Record{}, ErrEnd
	}
	rec := r.records[r.pos]
	r.pos++
	return rec, nil
}
