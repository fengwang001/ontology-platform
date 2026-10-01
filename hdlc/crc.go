package hdlc

// crc16X25 computes CRC-16/X-25 (reflected poly 0x1021, init 0xFFFF, xorout 0xFFFF).
func crc16X25(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}
