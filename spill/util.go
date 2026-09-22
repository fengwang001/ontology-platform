package spill

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

func runFileName(runID uint64) string {
	return fmt.Sprintf("run-%010d.dat", runID)
}

func appendU32(dst []byte, v uint32) []byte {
	return binary.BigEndian.AppendUint32(dst, v)
}

func crc32Append(dst, data []byte) []byte {
	return binary.BigEndian.AppendUint32(dst, crc32.Checksum(data, crcTable))
}

func crc32Value(data []byte) uint32 { return crc32.Checksum(data, crcTable) }
