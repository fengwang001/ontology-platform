package modbusrtu

// crc16 computes CRC-16/MODBUS over p: initial value 0xFFFF, reflected
// polynomial 0xA001. The check value over the ASCII string "123456789" is
// 0x4B37.
func crc16(p []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range p {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}
