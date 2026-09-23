package smj

import (
	"fmt"

	"ontology/row"
	"ontology/spillgrp"
	"ontology/stream"
)

// Pair 是一条连接结果，保留两侧原始到达信息。
type Pair struct {
	L row.Row
	R row.Row
}

// Stats 是可证明的推进次数统计。
type Stats struct {
	StreamReads  int // 两侧输入流读出的行数之和（== L+R）
	RescanReads  int // 重扫读出的右行数（内存+磁盘）
	DiskReads    int // 其中来自溢出文件的行数
	PeakResident int // 右侧分组历史最大驻留行数
	Spilled      int // 发生溢出的组数
}

// Emit 在执行中每产出一行被回调；流故障时此前已回调的结果即为保留前缀。
type Emit func(Pair)

// Executor 是排序合并连接执行器。
type Executor struct {
	left, right stream.Stream
	threshold   int
	seekFail    spillgrp.SeekFailFn

	lPeek *row.Row
	rPeek *row.Row

	owned []*spillgrp.Group

	StreamReads  int
	RescanReads  int
	DiskReads    int
	PeakResident int
	Spilled      int
}

// New 创建执行器；threshold 为右侧同键组驻留阈值。
func New(l, r stream.Stream, threshold int, seekFail spillgrp.SeekFailFn) *Executor {
	return &Executor{left: l, right: r, threshold: threshold, seekFail: seekFail}
}

// Run 执行连接。返回错误时（流故障/溢出损坏/Seek/逆序）已产出结果仍然有效，
// 并通过 leftKey/rightKey 上报两侧已消费到的键。
func (e *Executor) Run(emit Emit) (leftKey, rightKey string, err error) {
	defer func() {
		for _, g := range e.owned {
			g.Cleanup()
		}
	}()

	lrows, lkey, lok, lerr := e.readLeft()
	rgrp, rkey, rok, rerr := e.readRight()
	if lerr != nil || rerr != nil {
		return e.left.ConsumedKey(), e.right.ConsumedKey(), firstErr(lerr, rerr)
	}

	for lok && rok {
		switch {
		case lkey < rkey:
			lrows, lkey, lok, lerr = e.readLeft()
		case rkey < lkey:
			rgrp.Cleanup()
			rgrp, rkey, rok, rerr = e.readRight()
		default:
			if err = e.match(lrows, rgrp, emit); err != nil {
				return e.left.ConsumedKey(), e.right.ConsumedKey(), err
			}
			rgrp.Cleanup()
			lrows, lkey, lok, lerr = e.readLeft()
			if lerr == nil {
				rgrp, rkey, rok, rerr = e.readRight()
			}
		}
		if lerr != nil || rerr != nil {
			return e.left.ConsumedKey(), e.right.ConsumedKey(), firstErr(lerr, rerr)
		}
	}
	return e.left.ConsumedKey(), e.right.ConsumedKey(), nil
}

func firstErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// nextWithPeek 先消费 peek（跨组多读的一行），并做逆序检测。
func (e *Executor) nextWithPeek(s stream.Stream, peek **row.Row, side string,
	lastKey *string, haveLast *bool) (row.Row, bool, error) {
	if *peek != nil {
		r := *peek
		*peek = nil
		if *haveLast && r.Key < *lastKey {
			return row.Row{}, false, unsortedError(side, s.Index()-1, *lastKey, r.Key)
		}
		*lastKey = r.Key
		*haveLast = true
		return r, true, nil
	}
	r, ok, err := s.Next()
	if err != nil || !ok {
		return r, ok, err
	}
	e.StreamReads++
	if *haveLast && r.Key < *lastKey {
		if r.Key < *lastKey {
			return row.Row{}, false, unsortedError(side, s.Index()-1, *lastKey, r.Key)
		}
	}
	*lastKey = r.Key
	*haveLast = true
	return r, true, nil
}

// readLeft 读出左侧下一个同键组（全内存）。
func (e *Executor) readLeft() ([]row.Row, string, bool, error) {
	var last string
	var have bool
	r, ok, err := e.nextWithPeek(e.left, &e.lPeek, "left", &last, &have)
	if err != nil || !ok {
		return nil, "", ok, err
	}
	key := r.Key
	rows := []row.Row{r}
	for {
		r, ok, err = e.nextWithPeek(e.left, &e.lPeek, "left", &last, &have)
		if err != nil {
			return nil, key, false, err
		}
		if !ok {
			return rows, key, true, nil
		}
		if r.Key != key {
			e.lPeek = &r
			return rows, key, true, nil
		}
		rows = append(rows, r)
	}
}

// readRight 读出右侧下一个同键组（可能溢出），并密封。
func (e *Executor) readRight() (*spillgrp.Group, string, bool, error) {
	var last string
	var have bool
	r, ok, err := e.nextWithPeek(e.right, &e.rPeek, "right", &last, &have)
	if err != nil || !ok {
		return nil, "", ok, err
	}
	key := r.Key
	grp := spillgrp.NewGroup(key, e.threshold, e.seekFail)
	e.owned = append(e.owned, grp)
	if err := grp.Append(r); err != nil {
		return nil, key, false, err
	}
	for {
		r, ok, err = e.nextWithPeek(e.right, &e.rPeek, "right", &last, &have)
		if err != nil {
			return nil, key, false, err
		}
		if !ok {
			break
		}
		if r.Key != key {
			e.rPeek = &r
			break
		}
		if err := grp.Append(r); err != nil {
			return nil, key, false, err
		}
	}
	if err := grp.Seal(); err != nil {
		return nil, key, false, err
	}
	if grp.Spilled() {
		e.Spilled++
}
	if p := grp.Peak(); p > e.PeakResident {
		e.PeakResident = p
	}
	return grp, key, true, nil
}

// match 对等键两组做笛卡尔展开；右组对每个左行都从组起点重扫。
func (e *Executor) match(left []row.Row, rg *spillgrp.Group, emit Emit) error {
	for _, lr := range left {
		sc, err := rg.Rescan()
		if err != nil {
			return err
		}
		for {
			rr, ok, err := sc.Next()
			if err != nil {
				return err
			}
			if !ok {
				break
			}
			e.RescanReads++
			emit(Pair{L: lr, R: rr})
		}
		sc.Close()
	}
	e.DiskReads += rg.DiskReads()
	return nil
}

// Stats 拍取当前推进计数。
func (e *Executor) Stats() Stats {
	return Stats{
		StreamReads: e.StreamReads, RescanReads: e.RescanReads, DiskReads: e.DiskReads,
		PeakResident: e.PeakResident, Spilled: e.Spilled,
	}
}

// unsortedError 构造带侧别与位置的逆序错误。
func unsortedError(side string, idx int, prev, cur string) error {
	return fmt.Errorf("%w: side=%s index=%d %q>%q", ErrUnsorted, side, idx, prev, cur)
}
