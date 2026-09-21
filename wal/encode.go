package wal

import "encoding/binary"

// recordSize is the fixed encoded size of one Record in bytes.
const recordSize = 32

// encode serializes records into a single buffer so that Append can
// persist a whole batch with exactly one Sink.Write call.
func encode(recs []Record) []byte {
	buf := make([]byte, 0, len(recs)*recordSize)
	var tmp [recordSize]byte
	for _, r := range recs {
		binary.BigEndian.PutUint64(tmp[0:8], r.Txn)
		binary.BigEndian.PutUint64(tmp[8:16], uint64(int64(r.Shard)))
		binary.BigEndian.PutUint64(tmp[16:24], uint64(r.Delta))
		binary.BigEndian.PutUint64(tmp[24:32], 0) // reserved
		buf = append(buf, tmp[:]...)
	}
	return buf
}
