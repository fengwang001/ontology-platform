package walstore

import (
	"encoding/binary"
	"hash/crc32"
	"io"
)

const (
	// magic 作为帧头标记，随机垃圾命中完整合法帧的概率极低。
	magic0 byte = 0x57
	magic1 byte = 0x4C // "WL"

	recBatch      byte = 1
	recCheckpoint byte = 2

	headerLen = 2 + 1 + 4 // magic + type + payloadLen
	crcLen    = 4
)

type frame struct {
	typ     byte
	payload []byte
}

// encodeFrame 生成一条自描述、带 CRC 的完整帧。
func encodeFrame(typ byte, payload []byte) []byte {
	buf := make([]byte, headerLen+len(payload)+crcLen)
	buf[0] = magic0
	buf[1] = magic1
	buf[2] = typ
	binary.BigEndian.PutUint32(buf[3:7], uint32(len(payload)))
	copy(buf[headerLen:], payload)
	sum := crc32.ChecksumIEEE(buf[:headerLen+len(payload)])
	binary.BigEndian.PutUint32(buf[headerLen+len(payload):], sum)
	return buf
}

// encodeBatch 把一批键值编码为载荷，键值都允许任意非空串长度
// （空值以 vlen==0 表示，与“键不存在”无关）。
func encodeBatch(batch map[string]string) []byte {
	size := 4
	for k, v := range batch {
		size += 4 + len(k) + 4 + len(v)
	}
	buf := make([]byte, 0, size)
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], uint32(len(batch)))
	buf = append(buf, tmp[:]...)
	for k, v := range batch {
		binary.BigEndian.PutUint32(tmp[:], uint32(len(k)))
		buf = append(buf, tmp[:]...)
		buf = append(buf, k...)
		binary.BigEndian.PutUint32(tmp[:], uint32(len(v)))
		buf = append(buf, tmp[:]...)
		buf = append(buf, v...)
	}
	return buf
}

// decodeBatch 解析载荷，载荷损坏时返回 false。
func decodeBatch(p []byte) (map[string]string, bool) {
	if len(p) < 4 {
		return nil, false
	}
	n := binary.BigEndian.Uint32(p[:4])
	pos := 4
	out := make(map[string]string, n)
	for i := uint32(0); i < n; i++ {
		if pos+4 > len(p) {
			return nil, false
		}
		kLen := int(binary.BigEndian.Uint32(p[pos : pos+4]))
		pos += 4
		if pos+kLen+4 > len(p) {
			return nil, false
		}
		k := string(p[pos : pos+kLen])
		pos += kLen
		vLen := int(binary.BigEndian.Uint32(p[pos : pos+4]))
		pos += 4
		if pos+vLen > len(p) {
			return nil, false
		}
		v := string(p[pos : pos+vLen])
		pos += vLen
		out[k] = v
	}
	if pos != len(p) {
		return nil, false
	}
	return out, true
}

// readFrames 顺序读取帧，返回合法帧与合法前缀字节数。
// 遇到 EOF、短头、短载荷、CRC 不符即停止（半截尾部）。
func readFrames(r io.Reader) ([]frame, int, error) {
	var frames []frame
	valid := 0
	var header [headerLen]byte
	for {
		_, err := io.ReadFull(r, header[:])
		if err == io.EOF {
			return frames, valid, nil // 干净的文件尾
		}
		if err == io.ErrUnexpectedEOF {
			return frames, valid, nil // 半截头，丢弃
		}
		if err != nil {
			return frames, valid, err
		}
		if header[0] != magic0 || header[1] != magic1 {
			return frames, valid, nil // 非法头，按尾部垃圾丢弃
		}
		typ := header[2]
		pLen := int(binary.BigEndian.Uint32(header[3:7]))
		body := make([]byte, pLen+crcLen)
		if _, err := io.ReadFull(r, body); err != nil {
			return frames, valid, nil // 半截载荷/CRC，整条丢弃
		}
		payload := body[:pLen]
		want := binary.BigEndian.Uint32(body[pLen:])
		if crc32.ChecksumIEEE(append(header[:], payload...)) != want {
			return frames, valid, nil // CRC 不符，丢弃并停止
		}
		frames = append(frames, frame{typ: typ, payload: payload})
		valid += headerLen + len(body)
	}
}
