package spillgrp

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"

	"ontology/row"
)

const (
	magic      = "SMJSPILL"
	version    = byte(1)
	headerBase = 8 + 1 + 4 // magic + version + groupKeyLen
	crcLen     = 4
	lenLen     = 4
)

// headerLen 是指定组键的头部总长度（末尾还含 nrows）。
func headerLen(key string) int { return headerBase + len(key) + 4 }

// writeHeader 写入头部；nrows 由最终行数给出。
func writeHeader(w io.Writer, key string, nrows int) error {
	buf := make([]byte, headerLen(key))
	copy(buf, magic)
	buf[8] = version
	binary.LittleEndian.PutUint32(buf[9:], uint32(len(key)))
	copy(buf[13:], key)
	binary.LittleEndian.PutUint32(buf[13+len(key):], uint32(nrows))
	_, err := w.Write(buf)
	return err
}

// writeFrame 写入一帧：length | row | crc32(row)。
func writeFrame(w io.Writer, r row.Row) error {
	payload := row.Encode(r)
	buf := make([]byte, lenLen+len(payload)+crcLen)
	binary.LittleEndian.PutUint32(buf, uint32(len(payload)))
	copy(buf[lenLen:], payload)
	binary.LittleEndian.PutUint32(buf[lenLen+len(payload):], crc32.ChecksumIEEE(payload))
	_, err := w.Write(buf)
	return err
}

// readHeader 从头读并校验头部，返回组键、声明行数与头长。
func readHeader(b []byte) (key string, nrows int, hLen int, err error) {
	if len(b) < headerBase {
		return "", 0, 0, ErrHeader
	}
	if string(b[:8]) != magic || b[8] != version {
		return "", 0, 0, ErrHeader
	}
	kl := int(binary.LittleEndian.Uint32(b[9:]))
	hLen = headerBase + kl
	if len(b) < hLen+4 {
		return "", 0, hLen, ErrHeader
	}
	key = string(b[headerBase : headerBase+kl])
	nrows = int(binary.LittleEndian.Uint32(b[hLen:]))
	return key, nrows, hLen + 4, nil
}

// readFrame 从 b 读一帧，返回行与帧总长；损坏映射到哨兵错误。
func readFrame(b []byte) (r row.Row, frameLen int, err error) {
	if len(b) < lenLen {
		return row.Row{}, 0, ErrLength
	}
	pl := int(binary.LittleEndian.Uint32(b))
	bodyEnd := lenLen + pl
	if len(b) < bodyEnd {
		return row.Row{}, 0, ErrTruncated
	}
	if len(b) < bodyEnd+crcLen {
		return row.Row{}, 0, ErrCRC // 行体完整但 CRC 缺失/不完整
	}
	want := binary.LittleEndian.Uint32(b[bodyEnd:])
	if crc32.ChecksumIEEE(b[lenLen:bodyEnd]) != want {
		return row.Row{}, 0, ErrCRC
	}
	rr, _, ok := row.Decode(b[lenLen:bodyEnd])
	if !ok {
		return row.Row{}, 0, ErrCRC // 体在但行编码不合法，归入 CRC/数据损坏
	}
	return rr, bodyEnd + crcLen, nil
}

// ScanResult 是对溢出文件做恢复扫描的结果。
type ScanResult struct {
	Key       string    // 头部声明的组键
	NLines    int       // 头部声明的行数（可能为 0，若头部不完整）
	Recovered []row.Row // 最大可恢复前缀（不含半截行）
	HLen      int       // 头长度（截断分类用）
	Err       error     // 首个损坏错误；nil 表示文件完整
}

// ScanFile 扫描一个溢出文件（通常是被截断/损坏的副本），
// 返回最大可恢复前缀与首个可判定错误。
func ScanFile(name string) ScanResult {
	data, readErr := os.ReadFile(name)
	res := ScanResult{}
	if readErr != nil {
		res.Err = errors.Join(ErrHeader, readErr)
		return res
	}
	key, nlines, hLen, err := readHeader(data)
	res.Key, res.NLines, res.HLen = key, nlines, hLen
	if err != nil {
		res.Err = err
		return res
	}
	p := hLen
	for {
		if p == len(data) {
			break // 恰好完整结束
		}
		r, fl, ferr := readFrame(data[p:])
		if ferr != nil {
			res.Err = ferr
			return res
		}
		res.Recovered = append(res.Recovered, r)
		p += fl
	}
	if len(res.Recovered) != nlines {
		res.Err = ErrTruncated
	}
	return res
}
