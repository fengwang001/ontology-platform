package lsm

import (
	"bytes"
	"fmt"
)

// levelState 维护单层的文件集合、总字节数与压实起点记录。
// 零层文件按编号升序排列（允许任意重叠）；
// 非零层文件由 sortedIndex 维护（区间互不重叠，允许端点相接）。
type levelState struct {
	isL0       bool
	l0Files    []FileMeta // 仅零层使用，按 ID 升序
	ix         sortedIndex
	totalBytes int64
	// lastKey 是本层上次成功安装的压实计划的输入最大键；
	// 仅非零层使用。被放弃、被拒绝或被取消的计划不得更新它。
	lastKey    []byte
	hasLastKey bool
}

func newLevelState(isL0 bool) levelState {
	return levelState{isL0: isL0}
}

// count 返回层内文件数。
func (lv *levelState) count() int {
	if lv.isL0 {
		return len(lv.l0Files)
	}
	return len(lv.ix.files)
}

// files 返回层内全部文件：零层按 ID 升序，非零层按 (Smallest, Largest, ID) 升序。
func (lv *levelState) files() []FileMeta {
	if lv.isL0 {
		out := make([]FileMeta, len(lv.l0Files))
		copy(out, lv.l0Files)
		return out
	}
	return lv.ix.all()
}

// add 将文件加入层内（调用方已校验不变量）。
func (lv *levelState) add(f FileMeta) {
	if lv.isL0 {
		// 按 ID 升序插入。
		i := 0
		for i < len(lv.l0Files) && lv.l0Files[i].ID < f.ID {
			i++
		}
		lv.l0Files = append(lv.l0Files, FileMeta{})
		copy(lv.l0Files[i+1:], lv.l0Files[i:])
		lv.l0Files[i] = f
	} else {
		lv.ix.insert(f)
	}
	lv.totalBytes += f.Size
}

// remove 将文件从层内删除。
func (lv *levelState) remove(f FileMeta) {
	if lv.isL0 {
		for i, g := range lv.l0Files {
			if g.ID == f.ID {
				lv.l0Files = append(lv.l0Files[:i], lv.l0Files[i+1:]...)
				break
			}
		}
	} else {
		lv.ix.remove(f)
	}
	lv.totalBytes -= f.Size
}

// checkInvariant 校验新文件 f 放入本层后是否仍满足层不变量。
// 零层允许任意重叠，总是合法；非零层利用 Largest 单调不减的性质，
// 只需二分定位后检查直接前驱与直接后继：
// 前驱.Largest <= f.Smallest 且 f.Largest <= 后继.Smallest。
func (lv *levelState) checkInvariant(f FileMeta) error {
	if lv.isL0 {
		return nil
	}
	// 二分定位插入点。
	lo, hi := 0, len(lv.ix.files)
	for lo < hi {
		mid := (lo + hi) / 2
		if less(lv.ix.files[mid], f) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	pos := lo
	if pos > 0 {
		prev := lv.ix.files[pos-1]
		if bytes.Compare(prev.Largest, f.Smallest) > 0 {
			return fmt.Errorf("%w: file %d [%x,%x] overlaps predecessor file %d [%x,%x] in level %d",
				ErrInvariant, f.ID, f.Smallest, f.Largest, prev.ID, prev.Smallest, prev.Largest, f.Level)
		}
	}
	if pos < len(lv.ix.files) {
		next := lv.ix.files[pos]
		if next.ID != f.ID && bytes.Compare(f.Largest, next.Smallest) > 0 {
			return fmt.Errorf("%w: file %d [%x,%x] overlaps successor file %d [%x,%x] in level %d",
				ErrInvariant, f.ID, f.Smallest, f.Largest, next.ID, next.Smallest, next.Largest, f.Level)
		}
	}
	return nil
}
