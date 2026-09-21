package walstore

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

// 记录在 wal.log 中的物理布局：
//
//	magic(4) | payloadLen(4) | crc32(payload)(4) | payload
//
// payload 内部：uvarint(count)，随后 count 组 (uvarint klen, key, uvarint vlen, value)。
// 任何一段读不全或校验失败，都视为崩溃留下的尾部垃圾。
const (
	recordMagic   uint32 = 0x4B565231 // "KVR1"
	recordHdrSize        = 12
	maxPayload           = 1 << 30
)

var errCorruptRecord = errors.New("walstore: corrupt record")

// encodeRecord 把一批键值序列化为一条完整记录。
func encodeRecord(batch map[string]string) []byte {
	var payload []byte
	payload = binary.AppendUvarint(payload, uint64(len(batch)))
	for k, v := range batch {
		payload = binary.AppendUvarint(payload, uint64(len(k)))
		payload = append(payload, k...)
		payload = binary.AppendUvarint(payload, uint64(len(v)))
		payload = append(payload, v...)
	}
	var hdr [recordHdrSize]byte
	binary.LittleEndian.PutUint32(hdr[0:4], recordMagic)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	binary.LittleEndian.PutUint32(hdr[8:12], crc32.ChecksumIEEE(payload))
	rec := make([]byte, 0, recordHdrSize+len(payload))
	rec = append(rec, hdr[:]...)
	rec = append(rec, payload...)
	return rec
}

// readRecord 从 r 的当前位置读一条记录，返回其中的键值与整条记录的字节数。
// 到达干净 EOF 时返回 io.EOF；遇到不完整或校验失败的记录返回 errCorruptRecord。
func readRecord(r io.Reader) (map[string]string, int, error) {
	var hdr [recordHdrSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if err == io.EOF {
			return nil, 0, io.EOF
		}
		return nil, 0, errCorruptRecord // 半个头部：撕裂写
	}
	if binary.LittleEndian.Uint32(hdr[0:4]) != recordMagic {
		return nil, 0, errCorruptRecord
	}
	payloadLen := binary.LittleEndian.Uint32(hdr[4:8])
	if payloadLen == 0 || payloadLen > maxPayload {
		return nil, 0, errCorruptRecord
	}
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, 0, errCorruptRecord
	}
	if crc32.ChecksumIEEE(payload) != binary.LittleEndian.Uint32(hdr[8:12]) {
		return nil, 0, errCorruptRecord
	}
	batch, err := decodePayload(payload)
	if err != nil {
		return nil, 0, err
	}
	return batch, recordHdrSize + int(payloadLen), nil
}

func decodePayload(payload []byte) (map[string]string, error) {
	count, n := binary.Uvarint(payload)
	// count 允许为 0：空状态的检查点是一条合法的零条目记录。
	if n <= 0 || count > uint64(len(payload)) {
		return nil, errCorruptRecord
	}
	rest := payload[n:]
	batch := make(map[string]string, count)
	for i := uint64(0); i < count; i++ {
		key, val, remain, ok := readEntry(rest)
		if !ok {
			return nil, errCorruptRecord
		}
		batch[string(key)] = string(val)
		rest = remain
	}
	if len(rest) != 0 {
		return nil, errCorruptRecord
	}
	return batch, nil
}

// readEntry 从 buf 头部解析一个 (key, value)，返回剩余切片，不拷贝。
func readEntry(buf []byte) (key, val, rest []byte, ok bool) {
	klen, n := binary.Uvarint(buf)
	if n <= 0 || klen > uint64(len(buf)-n) {
		return nil, nil, nil, false
	}
	key = buf[n : n+int(klen)]
	buf = buf[n+int(klen):]
	vlen, m := binary.Uvarint(buf)
	if m <= 0 || vlen > uint64(len(buf)-m) {
		return nil, nil, nil, false
	}
	val = buf[m : m+int(vlen)]
	return key, val, buf[m+int(vlen):], true
}
