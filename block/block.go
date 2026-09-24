// Package block 实现前缀压缩块编解码：每 K 条设一个重启点存全量串，
// 其余条目只存「与前一条的公共前缀字节数 + 后缀」。前缀长度按字节计。
package block

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

var (
	ErrHeaderIncomplete, ErrRestartTable = errors.New("block: header incomplete"), errors.New("block: restart table incomplete")
	ErrEntryIncomplete, ErrCRC           = errors.New("block: entry incomplete"), errors.New("block: crc mismatch")
	ErrPrefixLen                         = errors.New("block: shared prefix length exceeds previous entry")
	ErrRestartOffset                     = errors.New("block: restart offset invalid, index disabled")
	ErrUnsorted                          = errors.New("block: entries not strictly increasing")
)

const headerSize, magic = 16, "PBLK"

// CommonPrefix 返回 a、b 的公共前缀长度，按字节计（见 DESIGN.md 第 5 节）。
func CommonPrefix(a, b string) (i int) {
	for i < min(len(a), len(b)) && a[i] == b[i] {
		i++
	}
	return i
}

// Encode 把严格递增的 entries 编码成一个块；k 为重启点间隔。
func Encode(entries []string, k int) ([]byte, error) {
	if k < 1 {
		k = 1
	}
	nR := (len(entries) + k - 1) / k
	buf := make([]byte, 0, 64+len(entries)*8)
	buf = append(buf, magic...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(k))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(entries)))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(nR))
	tablePos := len(buf)
	buf = append(buf, make([]byte, 4*nR)...)
	var restarts []uint32
	prev := ""
	for i, s := range entries {
		if i > 0 && prev >= s {
			return nil, fmt.Errorf("%w at index %d (%q !< %q)", ErrUnsorted, i, prev, s)
		}
		shared := 0
		if i%k == 0 {
			restarts = append(restarts, uint32(len(buf)))
		} else {
			shared = CommonPrefix(prev, s)
		}
		buf = binary.AppendUvarint(buf, uint64(shared))
		buf = binary.AppendUvarint(buf, uint64(len(s)-shared))
		buf = append(buf, s[shared:]...)
		prev = s
	}
	for i, off := range restarts {
		binary.LittleEndian.PutUint32(buf[tablePos+4*i:], off)
	}
	return binary.LittleEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf)), nil
}

// parseHeader 解析头部与重启点表，返回 K、条目数、表与条目区起点。
func parseHeader(data []byte) (k, count int, table []uint32, pos int, err error) {
	if len(data) < headerSize || string(data[:4]) != magic {
		return 0, 0, nil, 0, ErrHeaderIncomplete
	}
	k = int(binary.LittleEndian.Uint32(data[4:]))
	count = int(binary.LittleEndian.Uint32(data[8:]))
	nR := int(binary.LittleEndian.Uint32(data[12:]))
	if k < 1 {
		k = 1
	}
	if len(data) < headerSize+4*nR {
		return 0, 0, nil, 0, ErrRestartTable
	}
	table = make([]uint32, nR)
	for i := range table {
		table[i] = binary.LittleEndian.Uint32(data[headerSize+4*i:])
	}
	return k, count, table, headerSize + 4*nR, nil
}

// decodeOne 解码 pos 处的单条，prev 为前一条内容。
func decodeOne(data []byte, pos int, prev string) (string, int, error) {
	shared, n := binary.Uvarint(data[pos:])
	if n <= 0 {
		return "", pos, ErrEntryIncomplete
	}
	pos += n
	slen, n := binary.Uvarint(data[pos:])
	if n <= 0 {
		return "", pos, ErrEntryIncomplete
	}
	pos += n
	if shared > uint64(len(prev)) {
		return "", pos, ErrPrefixLen
	}
	if slen > uint64(len(data)-pos) {
		return "", pos, ErrEntryIncomplete
	}
	return prev[:shared] + string(data[pos:pos+int(slen)]), pos + int(slen), nil
}

// decodeEntries 顺序解码 count 条；出错时返回的条目即最大可恢复前缀。
func decodeEntries(data []byte, pos, count int) ([]string, []int, int, error) {
	entries := make([]string, 0, count)
	offs := make([]int, 0, count)
	prev := ""
	for i := 0; i < count; i++ {
		offs = append(offs, pos)
		s, np, err := decodeOne(data, pos, prev)
		if err != nil {
			return entries, offs, pos, err
		}
		pos = np
		if i > 0 && prev >= s {
			return entries, offs, pos, fmt.Errorf("%w at index %d", ErrUnsorted, i)
		}
		entries = append(entries, s)
		prev = s
	}
	return entries, offs, pos, nil
}

// Decode 完整解码并校验；ErrRestartOffset 时条目仍完整正确，仅索引失效。
func Decode(data []byte) ([]string, error) {
	k, count, table, pos, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	entries, offs, end, err := decodeEntries(data, pos, count)
	if err != nil {
		return entries, err
	}
	for i, off := range table {
		if i*k >= len(offs) || int(off) != offs[i*k] {
			return entries, fmt.Errorf("%w at restart %d", ErrRestartOffset, i)
		}
	}
	if len(data)-end != 4 ||
		crc32.ChecksumIEEE(data[:end]) != binary.LittleEndian.Uint32(data[end:]) {
		return entries, ErrCRC
	}
	return entries, nil
}

// DecodeEntry 解码第 idx 条，返回条目与实际解压次数（<=K，错位回退时更大）。
// 重启点偏移错位时回退到块首顺序解压，结果仍正确，错误标注 ErrRestartOffset。
func DecodeEntry(data []byte, idx int) (string, int, error) {
	k, count, table, pos, err := parseHeader(data)
	if err != nil {
		return "", 0, err
	}
	if idx < 0 || idx >= count {
		return "", 0, fmt.Errorf("block: index %d out of range [0,%d)", idx, count)
	}
	r := idx / k
	start, base, fallback := pos, 0, true
	if r < len(table) && int(table[r]) >= pos && int(table[r]) < len(data) {
		if shared, n := binary.Uvarint(data[table[r]:]); n > 0 && shared == 0 {
			start, base, fallback = int(table[r]), r*k, false
		}
	}
	prev, s, decoded := "", "", 0
	p := start
	for i := base; i <= idx; i++ {
		var err error
		s, p, err = decodeOne(data, p, prev)
		if err != nil {
			return "", decoded, err
		}
		prev = s
		decoded++
	}
	if fallback {
		return s, decoded, fmt.Errorf("%w at restart %d", ErrRestartOffset, r)
	}
	return s, decoded, nil
}

// DecodeN 从第 from 条（须为重启点或 0）起最多解码 n 条。
func DecodeN(data []byte, from, n int) ([]string, error) {
	k, count, table, pos, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	if from < 0 || from >= count || from%k != 0 {
		return nil, fmt.Errorf("block: DecodeN from %d not a restart point", from)
	}
	if off := int(table[from/k]); off >= pos && off < len(data) {
		pos = off
	}
	want := min(n, count-from)
	entries, _, _, err := decodeEntries(data, pos, want)
	return entries, err
}
