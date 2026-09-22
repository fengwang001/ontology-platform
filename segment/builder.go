package segment

import (
	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

// Builder 累积行并一次性写出不可变段。Builder 不可并发使用。
type Builder struct {
	opts  Options
	vals  []int64
	nulls []bool
}

// NewBuilder 创建建段器。
func NewBuilder(opts Options) *Builder {
	return &Builder{opts: opts.withDefaults()}
}

// Rows 返回已写入行数。
func (b *Builder) Rows() int { return len(b.vals) }

// Add 追加一个非空值。超限时拒绝且不改变已写入状态。
func (b *Builder) Add(v int64) error { return b.add(v, false) }

// AddNull 追加一个空值。超限时拒绝且不改变已写入状态。
func (b *Builder) AddNull() error { return b.add(0, true) }

func (b *Builder) add(v int64, null bool) error {
	if len(b.vals) >= b.opts.MaxRows {
		return ErrTooManyRows
	}
	if len(b.vals)/b.opts.RowGroupRows >= b.opts.MaxRowGroups {
		return ErrTooManyRowGroups
	}
	b.vals = append(b.vals, v)
	b.nulls = append(b.nulls, null)
	return nil
}

// Finish 把已写入的行编码为段并返回只读视图。
// 每个行组独立选择编码：字典编码字节更小时用字典，
// 字典基数超限或更大时回退位打包。
func (b *Builder) Finish() (*Segment, error) {
	rg := b.opts.RowGroupRows
	nGroups := (len(b.vals) + rg - 1) / rg
	var blocks [][]byte
	for g := 0; g < nGroups; g++ {
		lo, hi := g*rg, (g+1)*rg
		if hi > len(b.vals) {
			hi = len(b.vals)
		}
		blk, err := b.encodeGroup(b.vals[lo:hi], b.nulls[lo:hi])
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, blk)
	}
	return assemble(blocks, len(b.vals))
}

// encodeGroup 编码一个行组块：头部 + 统计 + 空值位图 + 数据块。
func (b *Builder) encodeGroup(vals []int64, nulls []bool) ([]byte, error) {
	st := statsOf(vals, nulls)
	var nonNull []int64
	for i, v := range vals {
		if !nulls[i] {
			nonNull = append(nonNull, v)
		}
	}
	enc, dictBytes, data := b.chooseEncoding(nonNull, st)

	var blk []byte
	// 头部：行数、空值数、编码、（字典时的）字典字节。
	blk = appendUvarint(blk, uint64(len(vals)))
	blk = appendUvarint(blk, uint64(st.Nulls))
	blk = append(blk, byte(enc))
	if enc == EncDict {
		blk = appendUvarint(blk, uint64(len(dictBytes)))
		blk = append(blk, dictBytes...)
	}
	// 统计：hasValue 标志 + min/max（空值不参与）。
	if st.HasValue {
		blk = append(blk, 1)
		blk = appendSvarint(blk, st.Min)
		blk = appendSvarint(blk, st.Max)
	} else {
		blk = append(blk, 0)
	}
	// 空值位图：bit i 置位表示第 i 行为空。
	bm := nullBitmap(nulls)
	blk = appendUvarint(blk, uint64(len(bm)))
	blk = append(blk, bm...)
	// 数据块。
	blk = appendUvarint(blk, uint64(len(data)))
	blk = append(blk, data...)
	return blk, nil
}

// chooseEncoding 为非空值序列选择编码并产出数据块。
// 字典基数超限（dict.ErrTooManyValues）时回退位打包。
func (b *Builder) chooseEncoding(nonNull []int64, st zone.Stats) (Encoding, []byte, []byte) {
	if len(nonNull) == 0 {
		return EncBitpack, nil, nil
	}
	bpData := encodeBitpackData(nonNull, st)
	d := dict.New(b.opts.MaxDictCard)
	codes := make([]uint64, 0, len(nonNull))
	dictOK := true
	for _, v := range nonNull {
		c, err := d.Add(v)
		if err != nil {
			dictOK = false // 基数超限：回退位打包
			break
		}
		codes = append(codes, uint64(c))
	}
	if dictOK {
		cw := bitpack.WidthFor(uint64(d.Cardinality() - 1))
		dictBytes := d.Freeze()
		dictDataLen := 1 + bitpack.PackedLen(cw, len(codes))
		if len(dictBytes)+dictDataLen < len(bpData) {
			data := []byte{byte(cw)}
			packed, _ := bitpack.Encode(codes, cw)
			return EncDict, dictBytes, append(data, packed...)
		}
	}
	return EncBitpack, nil, bpData
}

// encodeBitpackData 产出位打包数据块：1 字节位宽 + 偏移值位流。
func encodeBitpackData(nonNull []int64, st zone.Stats) []byte {
	width := bitpack.WidthFor(uint64(st.Max) - uint64(st.Min))
	offsets := make([]uint64, len(nonNull))
	for i, v := range nonNull {
		offsets[i] = uint64(v) - uint64(st.Min)
	}
	packed, _ := bitpack.Encode(offsets, width)
	return append([]byte{byte(width)}, packed...)
}
