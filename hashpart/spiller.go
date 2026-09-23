package hashpart

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
)

// Spiller 管理一个运行周期内的分区段文件：临时名写出 + 原子改名。
type Spiller struct {
	dir      string
	numParts int
	seq      []int
	segs     [][]string
	writes   int
	failOn   int
}

// NewSpiller 创建溢出器，并清理上次崩溃残留的半截临时文件。
func NewSpiller(dir string, numParts int) (*Spiller, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), tmpSuffix) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return nil, err
			}
		}
	}
	return &Spiller{dir: dir, numParts: numParts, seq: make([]int, numParts), segs: make([][]string, numParts)}, nil
}

// SetFailOnWrite 注入故障：第 k 次（1 起）WriteSegment 模拟写盘中途失败。
func (s *Spiller) SetFailOnWrite(k int) { s.failOn = k }

// Writes 返回已发生的写分区次数（含被注入失败的一次）。
func (s *Spiller) Writes() int { return s.writes }

// WriteSegment 把一个分区的一段记录落盘：先写临时名再原子改名。
func (s *Spiller) WriteSegment(part int, payloads [][]byte) error {
	s.writes++
	seg := s.seq[part]
	s.seq[part]++
	base := fmt.Sprintf("part-%04d-seg-%06d", part, seg)
	tmp := filepath.Join(s.dir, base+tmpSuffix)
	final := filepath.Join(s.dir, base)
	if s.writes == s.failOn {
		_ = os.WriteFile(tmp, []byte(magic), 0o644) // 半截临时文件
		return &FileError{part, 0, ErrWrite}
	}
	buf := make([]byte, 0, HeaderLen)
	buf = append(buf, magic...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(part))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(seg))
	buf = binary.LittleEndian.AppendUint64(buf, uint64(len(payloads)))
	for _, p := range payloads {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(p)))
		body := len(buf)
		buf = append(buf, p...)
		buf = binary.LittleEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf[body:]))
	}
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return &FileError{part, 0, ErrWrite}
	}
	if err := os.Rename(tmp, final); err != nil {
		return &FileError{part, 0, ErrWrite}
	}
	s.segs[part] = append(s.segs[part], final)
	return nil
}

// Segments 返回某分区已落盘的段文件路径，按段序号升序。
func (s *Spiller) Segments(part int) []string { return s.segs[part] }

// Cleanup 删除本次运行产生的全部段文件与残留临时文件。
func (s *Spiller) Cleanup() {
	for _, list := range s.segs {
		for _, f := range list {
			_ = os.Remove(f)
		}
	}
	s.segs = make([][]string, s.numParts)
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), tmpSuffix) {
			_ = os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
}

// Remaining 返回目录中残留的分区相关文件数（段文件 + 临时文件）。
func (s *Spiller) Remaining() int {
	entries, _ := os.ReadDir(s.dir)
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "part-") {
			n++
		}
	}
	return n
}
