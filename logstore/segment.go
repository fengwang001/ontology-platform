package logstore

// segment 是一个定长、只追加的段。
type segment struct {
	id     int
	used   uint64
	sealed bool
	maxTS  uint64
	blocks []*block
}

// free 返回段内剩余字节。
func (seg *segment) free(segmentSize uint64) uint64 {
	if seg.used > segmentSize {
		return 0
	}
	return segmentSize - seg.used
}

// append 把块追加到段尾。调用方必须保证剩余空间足够且段未封存。
func (seg *segment) append(b *block, size uint64) uint64 {
	offset := seg.used
	seg.blocks = append(seg.blocks, b)
	seg.used += size
	if b.ts > seg.maxTS {
		seg.maxTS = b.ts
	}
	return offset
}
