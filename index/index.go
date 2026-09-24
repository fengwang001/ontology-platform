// Package index 维护事件日志的稀疏锚点双索引：
// Append 登记事件，LocateSeq/LocateOffset 双向定位，Verify/Rebuild 校验与重建。
package index

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"ontology/seqmap"
)

// 可判定的哨兵错误，五类故障互不相同。
var (
	ErrBadSegment = errors.New("index: segmentSize 非正")
	ErrBadSeq     = errors.New("index: seq 非正或非严格递增")
	ErrBadLen     = errors.New("index: len 非正")
	ErrOutOfRange = errors.New("index: 目标早于首事件或晚于末事件")
	ErrNotExist   = errors.New("index: 范围内但该 Seq/Offset 从未出现")
	ErrCorrupt    = errors.New("index: 锚点表损坏")
)

// Index 是序号↔位点双索引，状态全在进程内存。
type Index struct {
	mu       sync.RWMutex
	seg      int64
	seqs     []int64      // 事件 Seq，严格递增
	offs     []int64      // 每条事件的起始 Offset
	total    int64        // 日志总长度 = 末事件结束 Offset
	anchors  seqmap.Map   // 稀疏锚点表
	lastScan atomic.Int64 // 最近一次 LocateSeq 从锚点起线性扫描的事件条数
}

// New 创建索引，segmentSize 非正时整体失败。
func New(segmentSize int64) (*Index, error) {
	if segmentSize <= 0 {
		return nil, ErrBadSegment
	}
	return &Index{seg: segmentSize}, nil
}

// Append 登记一条事件，返回其起始 Offset；非法输入整体失败、不留痕。
func (x *Index) Append(seq, ln int64) (int64, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if ln <= 0 {
		return 0, ErrBadLen
	}
	if n := len(x.seqs); seq <= 0 || (n > 0 && seq <= x.seqs[n-1]) {
		return 0, ErrBadSeq
	}
	off := x.total
	x.seqs = append(x.seqs, seq)
	x.offs = append(x.offs, off)
	x.total += ln
	if int64(len(x.seqs)-1)%x.seg == 0 { // 每个分段的第一条事件记锚点
		x.anchors.Add(seq, off)
	}
	return off, nil
}

// LocateSeq 给定 Seq 找起始 Offset：最后一个 Seq<=s 的锚点起线性扫描。
func (x *Index) LocateSeq(s int64) (int64, error) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	n := len(x.seqs)
	if n == 0 || s < x.seqs[0] || s > x.seqs[n-1] {
		return 0, ErrOutOfRange
	}
	a, ok := x.anchors.FloorSeq(s)
	if !ok {
		return 0, ErrCorrupt
	}
	start := sort.Search(n, func(i int) bool { return x.seqs[i] >= a.Seq })
	i := start
	for ; i < n && x.seqs[i] <= s; i++ {
		if x.seqs[i] == s {
			x.lastScan.Store(int64(i - start + 1))
			return x.offs[i], nil
		}
	}
	x.lastScan.Store(int64(i - start))
	return 0, ErrNotExist
}

// LocateOffset 给定 Offset 找 Seq：最后一个 Offset<=o 的锚点起线性扫描。
func (x *Index) LocateOffset(o int64) (int64, error) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	n := len(x.seqs)
	if n == 0 || o < 0 || o >= x.total {
		return 0, ErrOutOfRange
	}
	a, ok := x.anchors.FloorOffset(o)
	if !ok {
		return 0, ErrCorrupt
	}
	for i := sort.Search(n, func(i int) bool { return x.seqs[i] >= a.Seq }); i < n && x.offs[i] <= o; i++ {
		if x.offs[i] == o {
			return x.seqs[i], nil
		}
	}
	return 0, ErrNotExist
}

// Verify 校验锚点表自洽：Seq、Offset 严格递增，且每条锚点的 Offset
// 与从前任锚点起逐条扫描累加长度得到的结果一致。
func (x *Index) Verify() error {
	x.mu.RLock()
	defer x.mu.RUnlock()
	as := x.anchors.Anchors()
	if want := (len(x.seqs) + int(x.seg) - 1) / int(x.seg); len(as) != want {
		return ErrCorrupt
	}
	prevSeq, prevOff, prevIdx := int64(0), int64(0), 0
	for k, a := range as {
		idx := sort.Search(len(x.seqs), func(i int) bool { return x.seqs[i] >= a.Seq })
		if idx >= len(x.seqs) || x.seqs[idx] != a.Seq || int64(idx)%x.seg != 0 {
			return ErrCorrupt
		}
		if k == 0 && (idx != 0 || a.Offset != 0) {
			return ErrCorrupt
		}
		if k > 0 {
			if a.Seq <= prevSeq || a.Offset <= prevOff {
				return ErrCorrupt
			}
			scan := prevOff
			for j := prevIdx; j < idx; j++ {
				scan += x.offs[j+1] - x.offs[j]
			}
			if scan != a.Offset {
				return ErrCorrupt
			}
		}
		prevSeq, prevOff, prevIdx = a.Seq, a.Offset, idx
	}
	return nil
}

// Rebuild 从头重扫日志，重建全部锚点。
func (x *Index) Rebuild() error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.anchors.Reset()
	for i, s := range x.seqs {
		if int64(i)%x.seg == 0 {
			x.anchors.Add(s, x.offs[i])
		}
	}
	return nil
}
