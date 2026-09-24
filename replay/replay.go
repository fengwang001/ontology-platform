// Package replay 按事件序号范围定位并回放一个段目录。
package replay

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ontology/event"
	"ontology/segment"
	"ontology/sparse"
)

// ErrInvalidRange 表示 from > to。
var ErrInvalidRange = errors.New("replay: from > to")

var errStop = errors.New("replay: stop scan")

// Stats 是一次回放的非导出计数器快照（通过访问器读取）。
type Stats struct {
	skipped      uint64 // 定位阶段跳过的事件数
	bytesRead    uint64 // 顺序扫描阶段读取的记录字节数
	indexInvalid bool   // 索引被检出失效，已回退全段扫描
}

// Skipped 返回定位阶段跳过的事件数。
func (s Stats) Skipped() uint64 { return s.skipped }

// BytesRead 返回扫描阶段读取的字节数。
func (s Stats) BytesRead() uint64 { return s.bytesRead }

// IndexInvalid 报告索引是否被检出失效并回退。
func (s Stats) IndexInvalid() bool { return s.indexInvalid }

// Segment 描述目录中的一个段文件。
type Segment struct {
	Path     string
	FirstSeq uint64
	Count    uint64
}

// Segments 按首序号升序列出目录中的段。
func Segments(dir string) ([]Segment, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".seg") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var segs []Segment
	for _, n := range names {
		f, err := os.Open(filepath.Join(dir, n))
		if err != nil {
			return nil, err
		}
		h, err := segment.ReadHeader(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		segs = append(segs, Segment{Path: filepath.Join(dir, n), FirstSeq: h.FirstSeq, Count: h.Count})
	}
	return segs, nil
}

// Replay 严格回放 [from,to]：损坏会报错。
func Replay(dir string, from, to uint64, fn func(event.Event) error) (Stats, error) {
	return replay(dir, from, to, false, fn)
}

// ReplayPrefix 宽容回放：遇到正在写入的半条记录时视为前缀终点。
func ReplayPrefix(dir string, from, to uint64, fn func(event.Event) error) (Stats, error) {
	return replay(dir, from, to, true, fn)
}

// validateAnchor 校验锚点指向的记录可解码且序号一致。
func validateAnchor(f *os.File, a sparse.Anchor) bool {
	ok := false
	err := segment.Scan(f, int64(a.Offset), func(_ int64, e event.Event, _ int) error {
		ok = e.Seq == a.Seq
		return errStop
	})
	return err == errStop && ok
}

func replay(dir string, from, to uint64, tolerant bool, fn func(event.Event) error) (Stats, error) {
	var st Stats
	if from > to {
		return st, fmt.Errorf("%w: %d > %d", ErrInvalidRange, from, to)
	}
	segs, err := Segments(dir)
	if err != nil {
		return st, err
	}
	for _, s := range segs {
		end := s.FirstSeq + s.Count // 开区间
		if s.Count == 0 || end <= from || s.FirstSeq > to {
			continue
		}
		lo, hi := from, to
		if s.FirstSeq > lo {
			lo = s.FirstSeq
		}
		if end-1 < hi {
			hi = end - 1
		}
		rerr := replaySegment(s, lo, hi, tolerant, &st, fn)
		if rerr != nil {
			return st, rerr
		}
	}
	return st, nil
}

func replaySegment(s Segment, lo, hi uint64, tolerant bool, st *Stats, fn func(event.Event) error) error {
	f, err := os.Open(s.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	start := int64(segment.HeaderSize)
	if idx, ierr := sparse.Load(s.Path + ".idx"); ierr == nil {
		if a, ok := idx.Locate(lo); ok {
			if validateAnchor(f, a) {
				start = int64(a.Offset)
			} else {
				st.indexInvalid = true // 回退全段扫描
			}
		}
	}
	err = segment.Scan(f, start, func(_ int64, e event.Event, recLen int) error {
		if e.Seq > hi {
			return errStop
		}
		st.bytesRead += uint64(recLen)
		if e.Seq < lo {
			st.skipped++
			return nil
		}
		return fn(e)
	})
	if err == errStop {
		return nil
	}
	if err != nil && tolerant && isTruncation(err) {
		return nil
	}
	return err
}

func isTruncation(err error) bool {
	return errors.Is(err, segment.ErrHeaderIncomplete) ||
		errors.Is(err, segment.ErrLengthIncomplete) ||
		errors.Is(err, segment.ErrBodyIncomplete) ||
		errors.Is(err, segment.ErrCRCMismatch)
}
