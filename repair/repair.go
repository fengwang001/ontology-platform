// Package repair 提供段与索引的损坏检测、最大前缀恢复与索引重建。
package repair

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"

	"ontology/event"
	"ontology/replay"
	"ontology/segment"
	"ontology/sparse"
)

// ErrSeqGap 表示相邻段之间序号不连续。
var ErrSeqGap = errors.New("repair: sequence gap")

// GapError 指出缺口区间 [Lo, Hi]（含两端）。
type GapError struct {
	Lo, Hi uint64
}

func (e *GapError) Error() string {
	return fmt.Sprintf("repair: sequence gap [%d,%d]", e.Lo, e.Hi)
}

// Is 使 errors.Is(err, ErrSeqGap) 成立。
func (e *GapError) Is(target error) bool { return target == ErrSeqGap }

// Classify 检测段文件：完好返回 nil，否则返回四类可判定错误之一。
func Classify(segPath string) error {
	f, err := os.Open(segPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := segment.ReadHeader(f); err != nil {
		return err
	}
	return segment.Scan(f, segment.HeaderSize, func(int64, event.Event, int) error { return nil })
}

// Report 描述一次段修复。
type Report struct {
	Before      uint64 // 段头原记录条数
	After       uint64 // 实际可读条数
	TruncatedTo int64  // 截断到的字节长度
}

// RepairSegment 截到最后一条完整记录，并把段头条数改正为实际可读条数。
func RepairSegment(segPath string) (Report, error) {
	var rep Report
	f, err := os.OpenFile(segPath, os.O_RDWR, 0)
	if err != nil {
		return rep, err
	}
	defer f.Close()
	h, err := segment.ReadHeader(f)
	if err != nil {
		return rep, err // 头部不完整无法修复
	}
	rep.Before = h.Count
	lastEnd := int64(segment.HeaderSize)
	var n uint64
	scanErr := segment.Scan(f, segment.HeaderSize, func(off int64, _ event.Event, recLen int) error {
		lastEnd = off + int64(recLen)
		n++
		return nil
	})
	if scanErr == nil {
		if fi, err := f.Stat(); err == nil {
			lastEnd = fi.Size()
		}
	}
	rep.After = n
	rep.TruncatedTo = lastEnd
	if err := f.Truncate(lastEnd); err != nil {
		return rep, err
	}
	var cb [8]byte
	binary.LittleEndian.PutUint64(cb[:], n)
	if _, err := f.WriteAt(cb[:], segment.CountOffset); err != nil {
		return rep, err
	}
	return rep, nil
}

// RebuildIndex 从段文件重建稀疏索引（索引是纯加速结构，可丢弃）。
func RebuildIndex(segPath string, n uint32) error {
	return sparse.Build(segPath, segPath+".idx", n)
}

// CheckContinuity 校验目录内相邻段序号连续，发现缺口返回 *GapError。
func CheckContinuity(dir string) error {
	segs, err := replay.Segments(dir)
	if err != nil {
		return err
	}
	for i := 1; i < len(segs); i++ {
		prevEnd := segs[i-1].FirstSeq + segs[i-1].Count
		if prevEnd != segs[i].FirstSeq {
			lo := prevEnd
			hi := segs[i].FirstSeq
			if hi > 0 {
				hi--
			}
			return &GapError{Lo: lo, Hi: hi}
		}
	}
	return nil
}
