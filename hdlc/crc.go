package hdlc

func CRC16X25(payload []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range payload {
		crc ^= uint16(b)
		for bit := 0; bit < 8; bit++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}
