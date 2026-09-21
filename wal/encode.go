package wal

import "encoding/binary"

// recordSize 是单条记录编码后的固定字节数：
// Seq(8) + Txn(8) + Shard(8) + Delta(8)。
const recordSize = 32

// encode 把一组记录序列化为连续的字节流。
// 整组记录编码进同一个缓冲区，配合单次 Sink.Write 实现
// “要么全落要么全不落”的语义。
func encode(recs []Record) []byte {
	buf := make([]byte, len(recs)*recordSize)
	for i, r := range recs {
		off := i * recordSize
		binary.BigEndian.PutUint64(buf[off:], r.Seq)
		binary.BigEndian.PutUint64(buf[off+8:], r.Txn)
		binary.BigEndian.PutUint64(buf[off+16:], uint64(int64(r.Shard)))
		binary.BigEndian.PutUint64(buf[off+24:], uint64(r.Delta))
	}
	return buf
}

// decode 把字节流还原为记录切片，供需要读取持久化内容的调用方使用。
func decode(buf []byte) []Record {
	n := len(buf) / recordSize
	recs := make([]Record, 0, n)
	for i := 0; i < n; i++ {
		off := i * recordSize
		recs = append(recs, Record{
			Seq:   binary.BigEndian.Uint64(buf[off:]),
			Txn:   binary.BigEndian.Uint64(buf[off+8:]),
			Shard: int(int64(binary.BigEndian.Uint64(buf[off+16:]))),
			Delta: int64(binary.BigEndian.Uint64(buf[off+24:])),
		})
	}
	return recs
}
