package replication

import (
	"bufio"
	"encoding/binary"
	"hash/crc32"
	"io"
)

// WAL 帧布局（小端）：

//	magic  uint32  固定帧头
//	lsn    uint64
//	xid    uint64
//	kind   uint8
//	length uint32  payload 字节数
//	data   [length]byte
//	crc    uint32  对以上全部字节的 CRC32-IEEE
//
// 追加式写入；崩溃可能导致文件尾部留下不完整帧，恢复时忽略该撕裂帧。
// 帧体内部（头已完整但 CRC 不符）则视为损坏并返回 ErrCorrupted。

const (
	walMagic uint32 = 0x5245_5031 // "REP1"
	walHead         = 4 + 8 + 8 + 1 + 4
	walTail         = 4
)

var walCRCTable = crc32.MakeTable(crc32.IEEE)

// encodeRecord 把一条记录编码为一帧。
func encodeRecord(r Record) []byte {
	payloadLen := len(r.Payload)
	buf := make([]byte, walHead+payloadLen+walTail)
	binary.LittleEndian.PutUint32(buf[0:4], walMagic)
	binary.LittleEndian.PutUint64(buf[4:12], r.LSN)
	binary.LittleEndian.PutUint64(buf[12:20], r.XID)
	buf[20] = byte(r.Kind)
	binary.LittleEndian.PutUint32(buf[21:25], uint32(payloadLen))
	copy(buf[walHead:walHead+payloadLen], r.Payload)
	sum := crc32.Checksum(buf[:walHead+payloadLen], walCRCTable)
	binary.LittleEndian.PutUint32(buf[walHead+payloadLen:], sum)
	return buf
}

// readRecords 顺序读取 WAL 中的全部帧。
// 结尾不完整帧（崩溃撕裂）被静默忽略；中间帧魔数/CRC 错误返回 ErrCorrupted。
func readRecords(r io.Reader) ([]Record, error) {
	br := bufio.NewReader(r)
	var records []Record
	head := make([]byte, walHead)
	for {
		if _, err := io.ReadFull(br, head); err != nil {
			if err == io.EOF {
				return records, nil
			}
			if err == io.ErrUnexpectedEOF {
				return records, nil // 尾部撕裂帧：尚未写入头
			}
			return nil, err
		}
		magic := binary.LittleEndian.Uint32(head[0:4])
		if magic != walMagic {
			return nil, ErrCorrupted
		}
		lsn := binary.LittleEndian.Uint64(head[4:12])
		xid := binary.LittleEndian.Uint64(head[12:20])
		kind := Kind(head[20])
		payloadLen := binary.LittleEndian.Uint32(head[21:25])

		body := make([]byte, payloadLen+walTail)
		if _, err := io.ReadFull(br, body); err != nil {
			// 头完整但体/CRC 未写完：属于崩溃撕裂帧，忽略。
			if err == io.ErrUnexpectedEOF || err == io.EOF {
				return records, nil
			}
			return nil, err
		}
		checksum := crc32.Checksum(append(append([]byte(nil), head...), body[:payloadLen]...), walCRCTable)
		if binary.LittleEndian.Uint32(body[payloadLen:]) != checksum {
			return nil, ErrCorrupted
		}
		var payload []byte
		if payloadLen > 0 {
			payload = body[:payloadLen]
		}
		records = append(records, Record{LSN: lsn, XID: xid, Kind: kind, Payload: payload})
	}
}
