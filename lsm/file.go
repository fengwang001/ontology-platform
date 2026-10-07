package lsm

import (
	"bytes"
	"fmt"
)

// FileMeta 描述一个已登记文件的元数据。
// 用户键区间为闭区间 [Smallest, Largest]，要求 Smallest <= Largest（按字节序）。
// 编号越小的文件越旧。
type FileMeta struct {
	ID       uint64
	Level    int
	Smallest []byte
	Largest  []byte
	Size     int64
}

// validate 校验文件元数据本身的合法性（不涉及与其他文件的关系）。
func (f FileMeta) validate(numLevels int) error {
	if f.Level < 0 || f.Level >= numLevels {
		return fmt.Errorf("%w: file %d level %d out of range [0,%d)", ErrInvalidArgument, f.ID, f.Level, numLevels)
	}
	if f.Smallest == nil || f.Largest == nil {
		return fmt.Errorf("%w: file %d has nil key bound", ErrInvalidArgument, f.ID)
	}
	if bytes.Compare(f.Smallest, f.Largest) > 0 {
		return fmt.Errorf("%w: file %d smallest key greater than largest key", ErrInvalidArgument, f.ID)
	}
	if f.Size < 0 {
		return fmt.Errorf("%w: file %d has negative size %d", ErrInvalidArgument, f.ID, f.Size)
	}
	return nil
}

// overlaps 判断闭区间 [aLo,aHi] 与 [bLo,bHi] 是否有重叠，端点相等也算重叠。
func overlaps(aLo, aHi, bLo, bHi []byte) bool {
	return bytes.Compare(aLo, bHi) <= 0 && bytes.Compare(bLo, aHi) <= 0
}

// interval 表示一个闭区间键范围。
type interval struct {
	lo []byte
	hi []byte
}

// extend 将区间扩展到包含 [lo,hi]。
func (in *interval) extend(lo, hi []byte) {
	if in.lo == nil || bytes.Compare(lo, in.lo) < 0 {
		in.lo = lo
	}
	if in.hi == nil || bytes.Compare(hi, in.hi) > 0 {
		in.hi = hi
	}
}
