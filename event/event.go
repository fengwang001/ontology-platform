// Package event 定义日志事件及其自描述帧编解码。
package event

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

// Event 是一条只追加事件：单调递增序号与不透明载荷。
type Event struct {
	Seq     uint64
	Payload []byte
}

var crcTable = crc32.MakeTable(crc32.IEEE)

var (
	// ErrTruncatedLength 长度前缀 uvarint 不完整。
	ErrTruncatedLength = errors.New("event: truncated length prefix")
	// ErrTruncatedBody 序号、载荷或 CRC 不足。
	ErrTruncatedBody = errors.New("event: truncated event body")
	// ErrCRCMismatch 记录完整但校验失败。
	ErrCRCMismatch = errors.New("event: crc mismatch")
)

// AppendFrame 把事件追加到 dst，返回新切片。
// 帧布局：载荷长度 uvarint | 序号 uvarint | 载荷 | CRC32BE(序号+载荷)。
func AppendFrame(dst []byte, e Event) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], uint64(len(e.Payload)))
	dst = append(dst, buf[:n]...)
	n = binary.PutUvarint(buf[:], e.Seq)
	dst = append(dst, buf[:n]...)
	dst = append(dst, e.Payload...)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], checksum(e.Seq, e.Payload))
	return append(dst, sum[:]...)
}

func checksum(seq uint64, payload []byte) uint32 {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], seq)
	h := crc32.New(crcTable)
	h.Write(buf[:n])
	h.Write(payload)
	return h.Sum32()
}

// ReadFrame 从 r 顺序读出一帧。io.EOF 且首字节都没有时原样返回。
func ReadFrame(r io.ByteReader) (Event, error) {
	payloadLen, err := binary.ReadUvarint(r)
	if err != nil {
		return Event{}, err
	}
	seq, err := binary.ReadUvarint(r)
	if err != nil {
		return Event{}, ErrTruncatedBody
	}
	payload := make([]byte, payloadLen)
	if err := readFull(r, payload); err != nil {
		return Event{}, ErrTruncatedBody
	}
	var sum [4]byte
	if err := readFull(r, sum[:]); err != nil {
		return Event{}, ErrTruncatedBody
	}
	if binary.BigEndian.Uint32(sum[:]) != checksum(seq, payload) {
		return Event{}, ErrCRCMismatch
	}
	return Event{Seq: seq, Payload: payload}, nil
}

func readFull(r io.ByteReader, buf []byte) error {
	if rr, ok := r.(io.Reader); ok {
		_, err := io.ReadFull(rr, buf)
		return err
	}
	for i := range buf {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		buf[i] = b
	}
	return nil
}
