package spillgrp

import (
	"encoding/binary"
	"io"

	"ontology/row"
)

// putHeaderTail 就地把 groupKeyLen/groupKey/nrows 填入头部缓冲。
func putHeaderTail(hdr []byte, key string, nrows int) {
	binary.LittleEndian.PutUint32(hdr[9:], uint32(len(key)))
	copy(hdr[13:], key)
	binary.LittleEndian.PutUint32(hdr[13+len(key):], uint32(nrows))
}

// Scan 是一次回退重扫的迭代器：先按到达序产出内存段，再顺序产出磁盘段。
type Scan struct {
	g        *Group
	idx      int
	r        io.Reader // 磁盘帧读取器（Seek 之后）
	diskLeft int       // 磁盘段剩余帧数
}

// Rescan 回到该组起点。磁盘组先 Seek 回字节偏移 start，绝不按行号推算。
func (g *Group) Rescan() (*Scan, error) {
	g.waitSealed()
	g.mu.Lock()
	defer g.mu.Unlock()
	s := &Scan{g: g}
	if !g.spill {
		return s, nil
	}
	g.rescans++
	attempt := g.rescans
	if g.failSeek != nil && g.failSeek(g.key, attempt) {
		return nil, seekError(g.key)
	}
	if sk, ok := g.f.(Seeker); ok {
		if _, err := sk.Seek(g.start, io.SeekStart); err != nil {
			return nil, seekError(g.key)
		}
	} else {
		return nil, seekError(g.key)
	}
	s.r = g.f
	s.diskLeft = g.written
	return s, nil
}

// Next 按组内到达序产出下一行；磁盘帧损坏时返回可判定错误。
func (s *Scan) Next() (row.Row, bool, error) {
	s.g.mu.Lock()
	mem := s.g.mem
	spilled := s.g.spill
	s.g.mu.Unlock()
	if s.idx < len(mem) {
		r := mem[s.idx]
		s.idx++
		return r, true, nil
	}
	if !spilled || s.diskLeft == 0 {
		return row.Row{}, false, nil
	}
	r, err := readOneFrame(s.r)
	if err != nil {
		return row.Row{}, false, err
	}
	s.diskLeft--
	s.g.mu.Lock()
	s.g.diskRead++
	s.g.mu.Unlock()
	return r, true, nil
}

// Close 对复用的文件句柄无操作。
func (s *Scan) Close() error { return nil }

// readOneFrame 从 r 精确读取一帧并校验。
func readOneFrame(r io.Reader) (row.Row, error) {
	lb := make([]byte, lenLen)
	if _, err := io.ReadFull(r, lb); err != nil {
		return row.Row{}, ErrLength
	}
	pl := int(binary.LittleEndian.Uint32(lb))
	buf := make([]byte, pl+crcLen)
	if n, err := io.ReadFull(r, buf); err != nil {
		if n < pl {
			return row.Row{}, ErrTruncated
		}
		return row.Row{}, ErrCRC
	}
	fr, _, ferr := readFrame(append(lb, buf...))
	return fr, ferr
}
