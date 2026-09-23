// Package persist 把网格索引落盘并读回：自描述头 + 逐格记录 + CRC32，
// 支持截断分类、最大前缀恢复与结构不变量校验。
package persist

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"math"
	"os"

	"ontology/cell"
	"ontology/geom"
	"ontology/grid"
)

// 文件布局：24 字节头 + 先序排列的格记录。
// header: magic[8] | version u32 | cellCount u32 | capacity u32 | crc u32
// record: bounds f64×4 | flags u8 | nPoints u32 | points f64×2×n | crc u32
const (
	headerSize = 24
	fixedSize  = 37 // 记录定长部分：32 字节边界 + 1 标志 + 4 点数
	pointSize  = 16
	version    = 1
)

var magic = []byte("OGRIDv1\n")

// Kind 是读回失败的分类。
type Kind int

const (
	KindNone Kind = iota
	KindHeaderShort       // 头部不完整
	KindBadHeader         // 魔数或版本无效
	KindRecordHeaderShort // 格记录头不完整
	KindPointDataShort    // 点数据不完整
	KindCRCMismatch       // CRC 不匹配
	KindMissingChildren   // 父格声称已分裂但子格缺失
	KindChildOutOfBounds  // 子格边界超出父格
	KindNoIndex           // 索引文件不存在
)

// Error 描述一次读回失败，含出错的格序号与越界维度。
type Error struct {
	Kind Kind
	Cell int
	Dim  string
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindHeaderShort:
		return "头部不完整"
	case KindBadHeader:
		return "头部无效：魔数或版本不符"
	case KindRecordHeaderShort:
		return "格记录头不完整"
	case KindPointDataShort:
		return "点数据不完整"
	case KindCRCMismatch:
		return "CRC 不匹配"
	case KindMissingChildren:
		return "结构破坏：格 " + itoa(e.Cell) + " 声称已分裂但子格缺失"
	case KindChildOutOfBounds:
		return "结构破坏：格 " + itoa(e.Cell) + " 的子格在 " + e.Dim + " 维越界"
	case KindNoIndex:
		return "索引文件不存在（残留临时文件已清理）"
	}
	return "未知错误"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// rec 是一条待落盘的格记录。
type rec struct {
	bounds  geom.Rect
	divided bool
	points  []geom.Point
}

// Save 把网格写入 path：先写临时文件再原子改名。
func Save(g *grid.Grid, path string) error {
	var recs []rec
	capacity := 0
	g.ReadView(func(root *cell.Cell) {
		capacity = root.Capacity()
		walkRecs(root, &recs)
	})
	var buf bytes.Buffer
	hdr := make([]byte, headerSize)
	copy(hdr, magic)
	binary.LittleEndian.PutUint32(hdr[8:], version)
	binary.LittleEndian.PutUint32(hdr[12:], uint32(len(recs)))
	binary.LittleEndian.PutUint32(hdr[16:], uint32(capacity))
	binary.LittleEndian.PutUint32(hdr[20:], crc32.ChecksumIEEE(hdr[:20]))
	buf.Write(hdr)
	for _, r := range recs {
		writeRec(&buf, r)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func walkRecs(c *cell.Cell, out *[]rec) {
	*out = append(*out, rec{bounds: c.Bounds, divided: c.Divided, points: c.Points})
	for _, ch := range c.Children {
		if ch != nil {
			walkRecs(ch, out)
		}
	}
}

func writeRec(buf *bytes.Buffer, r rec) {
	start := buf.Len()
	var fixed [fixedSize]byte
	binary.LittleEndian.PutUint64(fixed[0:], math.Float64bits(r.bounds.X0))
	binary.LittleEndian.PutUint64(fixed[8:], math.Float64bits(r.bounds.Y0))
	binary.LittleEndian.PutUint64(fixed[16:], math.Float64bits(r.bounds.X1))
	binary.LittleEndian.PutUint64(fixed[24:], math.Float64bits(r.bounds.Y1))
	if r.divided {
		fixed[32] = 1
	}
	binary.LittleEndian.PutUint32(fixed[33:], uint32(len(r.points)))
	buf.Write(fixed[:])
	for _, p := range r.points {
		var pb [pointSize]byte
		binary.LittleEndian.PutUint64(pb[0:], math.Float64bits(p.X))
		binary.LittleEndian.PutUint64(pb[8:], math.Float64bits(p.Y))
		buf.Write(pb[:])
	}
	var crc [4]byte
	binary.LittleEndian.PutUint32(crc[:], crc32.ChecksumIEEE(buf.Bytes()[start:]))
	buf.Write(crc[:])
}
