// Package persist 把 LSH 索引落盘并读回：自描述头+超平面参数+桶表+CRC32，支持截断分类与恢复。
package persist

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
)

// HeaderSize 是文件头字节数：magic4 + ver2 + dim/bits/tables/nvecs 各4 + seed8。
const HeaderSize = 30

var ErrTruncHeader = errors.New("persist: truncated header")
var ErrTruncPlanes = errors.New("persist: truncated hyperplane section")
var ErrTruncBuckets = errors.New("persist: truncated bucket section")
var ErrCRC = errors.New("persist: crc32 mismatch")
var ErrDegenerate = errors.New("persist: degenerate hyperplane (zero normal)")
var ErrBadMagic = errors.New("persist: bad magic or version")

// Index 是索引的可序列化结构（不含向量本体，ID 语义由调用方保证）。
type Index struct {
	Dim, Bits, NVecs int
	Seed             int64
	Planes           [][][]float64 // [表][位][维] 法向量
	Tables           []map[uint64][]int
}

// Stats 记录恢复结果：完整恢复的表数与被剔除的悬挂 ID 数。
type Stats struct{ TablesRecovered, DanglingIDs int }

func (idx *Index) encode() []byte { // 序列化（不含 CRC）
	buf := []byte{'O', 'L', 'S', 'H', 1, 0} // magic + ver=1
	for _, v := range []int{idx.Dim, idx.Bits, len(idx.Planes), idx.NVecs} {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(v))
	}
	buf = binary.LittleEndian.AppendUint64(buf, uint64(idx.Seed))
	for _, tab := range idx.Planes {
		for _, p := range tab {
			for _, x := range p {
				buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(x))
			}
		}
	}
	for _, tab := range idx.Tables {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(tab)))
		for sig, ids := range tab {
			buf = binary.LittleEndian.AppendUint64(buf, sig)
			buf = binary.LittleEndian.AppendUint32(buf, uint32(len(ids)))
			for _, id := range ids {
				buf = binary.LittleEndian.AppendUint32(buf, uint32(id))
			}
		}
	}
	return buf
}

// Save 把索引写入 path（末尾追加 CRC32）。
func Save(path string, idx *Index) error {
	buf := idx.encode()
	buf = binary.LittleEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf))
	return os.WriteFile(path, buf, 0o600)
}

type cursor struct {
	b   []byte
	off int
}

func (c *cursor) u32() (uint32, bool) {
	if c.off+4 <= len(c.b) {
		v := binary.LittleEndian.Uint32(c.b[c.off:])
		c.off += 4
		return v, true
	}
	return 0, false
}

func (c *cursor) u64() (uint64, bool) {
	if c.off+8 <= len(c.b) {
		v := binary.LittleEndian.Uint64(c.b[c.off:])
		c.off += 8
		return v, true
	}
	return 0, false
}

func parseHeader(b []byte) (*Index, int, error) {
	if len(b) < HeaderSize {
		return nil, 0, ErrTruncHeader
	}
	if string(b[:4]) != "OLSH" || binary.LittleEndian.Uint16(b[4:]) != 1 {
		return nil, 0, ErrBadMagic
	}
	g := func(o int) int { return int(binary.LittleEndian.Uint32(b[o:])) }
	idx := &Index{Dim: g(6), Bits: g(10), NVecs: g(18), Seed: int64(binary.LittleEndian.Uint64(b[22:]))}
	return idx, g(14), nil
}

// parseBuckets 解析一张桶表：计数按剩余字节校验上限；悬挂 ID 剔除并计数。
func parseBuckets(c *cursor, nvecs int, dangling *int) (map[uint64][]int, bool) {
	nb, ok := c.u32()
	if !ok || uint64(nb) > uint64(len(c.b)-c.off)/12 {
		return nil, false
	}
	tab := make(map[uint64][]int, nb)
	for range nb {
		sig, ok1 := c.u64()
		cnt, ok2 := c.u32()
		if !ok1 || !ok2 || uint64(cnt) > uint64(len(c.b)-c.off)/4 {
			return nil, false
		}
		ids := make([]int, 0, cnt)
		for range cnt {
			v, ok := c.u32()
			if !ok {
				return nil, false
			}
			if id := int(v); id < nvecs {
				ids = append(ids, id)
			} else {
				(*dangling)++
			}
		}
		tab[sig] = ids
	}
	return tab, true
}

// parse 解析文件体：strict（Load）按区报错并做退化/CRC 校验；否则（Recover）恢复最大前缀。
func parse(b []byte, strict bool) (*Index, Stats, error) {
	idx, nTab, err := parseHeader(b)
	if err != nil {
		return nil, Stats{}, err
	}
	var st Stats
	if len(b) < HeaderSize+nTab*idx.Bits*idx.Dim*8 {
		if strict {
			return nil, st, ErrTruncPlanes
		}
		return idx, st, nil // 超平面区不完整：无可恢复表
	}
	c := &cursor{b: b, off: HeaderSize}
	planes := make([][][]float64, 0, nTab)
	for t := 0; t < nTab; t++ { // 超平面区长度已预检，u64 不会短缺
		tab := make([][]float64, idx.Bits)
		for i := range tab {
			p := make([]float64, idx.Dim)
			var norm2 float64
			for j := range p {
				u, _ := c.u64()
				p[j] = math.Float64frombits(u)
				norm2 += p[j] * p[j]
			}
			if strict && norm2 == 0 {
				return nil, st, fmt.Errorf("%w: table %d bit %d", ErrDegenerate, t, i)
			}
			tab[i] = p
		}
		planes = append(planes, tab)
	}
	for t := 0; t < nTab; t++ {
		tab, ok := parseBuckets(c, idx.NVecs, &st.DanglingIDs)
		if !ok {
			if strict {
				return nil, st, ErrTruncBuckets
			}
			break
		}
		idx.Planes = append(idx.Planes, planes[t])
		idx.Tables = append(idx.Tables, tab)
		st.TablesRecovered++
	}
	if strict {
		crc, ok := c.u32()
		if !ok || c.off != len(b) || crc != crc32.ChecksumIEEE(b[:c.off-4]) {
			return nil, st, ErrCRC
		}
	}
	return idx, st, nil
}

func readParse(path string, strict bool) (*Index, Stats, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, Stats{}, err
	}
	return parse(b, strict)
}

// Load 严格读回索引：截断按区分类，退化超平面与 CRC 不匹配均可判定。
func Load(path string) (*Index, error) {
	idx, _, err := readParse(path, true)
	return idx, err
}

// Recover 恢复最大可恢复前缀：忽略 CRC，逐张纳入完整桶表，结果无悬挂 ID。
func Recover(path string) (*Index, Stats, error) { return readParse(path, false) }
