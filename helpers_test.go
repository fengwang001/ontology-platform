package snapshot

import "hash/crc32"

func crc32Region(b []byte) uint32 { return crc32.ChecksumIEEE(b) }
