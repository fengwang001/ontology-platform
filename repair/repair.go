// Package repair 提供段与索引的损坏检测、最大前缀恢复与索引重建。
package repair

import (
	"errors"
	"fmt"
	"io"
	"os"

	"ontology/segment"
	"ontology/sparse"
)

// ErrSeqGap 表示相邻段之间序号不连续。
var ErrSeqGap = errors.New("repair: sequence gap between segments")

// GapError 描述检出的序号缺口区间 [MissingFrom, MissingTo]。
type GapError struct {
	MissingFrom uint64
	MissingTo   uint64
}

func (e *GapError) Error() string {
	return fmt.Sprintf("%v: missing seqs [%d, %d]", ErrSeqGap, e.MissingFrom, e.MissingTo)
}

// Unwrap 使 errors.Is(err, ErrSeqGap) 成立。
func (e *GapError) Unwrap() error { return ErrSeqGap }

// Classify 扫描段文件，返回首个损坏处的可判定错误；文件完整返回 nil。
func Classify(path string) error {
	sc, err := segment.NewScanner(path)
	if err != nil {
		return err
	}
	defer sc.Close()
	for i := uint64(0); i < sc.Header.Count; i++ {
		if _, err := sc.Next(); err != nil {
			if errors.Is(err, io.EOF) { // 头承诺的条数未到文件尾就没了
				return segment.ErrLengthPrefixIncomplete
			}
			return err
		}
	}
	return nil
}

// Report 记录一次段修复。
type Report struct {
	OldCount uint64 // 段头原事件数
	NewCount uint64 // 改正后的实际可读条数
}

// RepairSegment 把段截断到最大可恢复前缀，并把段头 count 改正为实际条数，
// 随后重建该段索引。文件本就完整时仅重建索引。
func RepairSegment(path string, every int) (Report, error) {
	sc, err := segment.NewScanner(path)
	if err != nil {
		return Report{}, err
	}
	good := uint64(0)
	for good = 0; good < sc.Header.Count; good++ {
		if _, err := sc.Next(); err != nil {
			break
		}
	}
	end := sc.Off()
	old, first := sc.Header.Count, sc.Header.FirstSeq
	sc.Close()
	rep := Report{OldCount: old, NewCount: good}
	if good != old {
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return rep, err
		}
		defer f.Close()
		if err := f.Truncate(end); err != nil {
			return rep, err
		}
		hdr := segment.EncodeHeader(segment.Header{FirstSeq: first, Count: good})
		if _, err := f.WriteAt(hdr, 0); err != nil {
			return rep, err
		}
	}
	return rep, RebuildIndex(path, every)
}

// RebuildIndex 从段文件完整重建稀疏索引（索引是可丢弃的加速结构）。
func RebuildIndex(segPath string, every int) error {
	sc, err := segment.NewScanner(segPath)
	if err != nil {
		return err
	}
	defer sc.Close()
	idx := sparse.Index{Every: every}
	for i := uint64(0); i < sc.Header.Count; i++ {
		off := sc.Off()
		ev, err := sc.Next()
		if err != nil {
			return err
		}
		if i%uint64(every) == 0 {
			idx.Anchors = append(idx.Anchors, sparse.Anchor{Seq: ev.Seq, Offset: off})
		}
	}
	return sparse.WriteFile(segment.IndexPath(segPath), idx)
}

// CheckContinuity 校验目录内相邻段序号连续，发现缺口返回 *GapError。
func CheckContinuity(dir string) error {
	segs, err := segment.ListSegments(dir)
	if err != nil {
		return err
	}
	var prev segment.Header
	for i, p := range segs {
		sc, err := segment.NewScanner(p)
		if err != nil {
			return err
		}
		h := sc.Header
		sc.Close()
		if i > 0 && prev.FirstSeq+prev.Count != h.FirstSeq {
			return &GapError{MissingFrom: prev.FirstSeq + prev.Count, MissingTo: h.FirstSeq - 1}
		}
		prev = h
	}
	return nil
}
