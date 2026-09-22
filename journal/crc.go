package journal

import "hash/crc32"

// crcTable is the standard IEEE polynomial table used for frame checksums.
var crcTable = crc32.MakeTable(crc32.IEEE)

func crcOf(p []byte) uint32 { return crc32.Checksum(p, crcTable) }
