package intelhex

// record 是一行 Intel HEX 记录解析后的结果。
type record struct {
	length byte   // LL：声明的数据字节数
	offset uint16 // AAAA：16 位偏移
	rtype  byte   // TT：记录类型
	data   []byte // LL 字节数据（底层数组每次解析独立）
}

// parseRecord 解析一行文本并执行与状态无关的校验。
// 返回的错误为裸哨兵错误（ErrSyntax 等），由调用方包装行序号。
// 拒绝优先级：语法 -> 校验和 -> 未知类型 -> 类型相关 LL/AAAA -> 64KiB 越界。
func parseRecord(line string) (record, error) {
	if len(line) == 0 || line[0] != ':' {
		return record{}, ErrSyntax
	}
	hex := line[1:]
	// 冒号之后必须是偶数个十六进制字符：LL AAAA TT 为头 4 字节，
	// 之后为 LL 字节数据加 1 字节校验和。
	if len(hex) < 10 || len(hex)%2 != 0 {
		return record{}, ErrSyntax
	}

	raw := make([]byte, len(hex)/2)
	for i := 0; i < len(hex); i += 2 {
		hi, ok1 := hexNibble(hex[i])
		lo, ok2 := hexNibble(hex[i+1])
		if !ok1 || !ok2 {
			return record{}, ErrSyntax
		}
		raw[i/2] = hi<<4 | lo
	}

	rec := record{
		length: raw[0],
		offset: uint16(raw[1])<<8 | uint16(raw[2]),
		rtype:  raw[3],
		data:   raw[4 : len(raw)-1],
	}

	// 实际数据长度必须与 LL 相符。
	if len(rec.data) != int(rec.length) {
		return record{}, ErrSyntax
	}

	// LL 到 CC 全部字节之和模 256 必须为 0（raw 恰为 LL..CC）。
	var sum byte
	for _, b := range raw {
		sum += b
	}
	if sum != 0 {
		return record{}, ErrChecksum
	}

	switch rec.rtype {
	case 0x00: // 数据记录：LL 可为 0，偏移不做额外限制（越界由调用方判定）
	case 0x01: // 文件结束：LL=0，AAAA=0
		if rec.length != 0 || rec.offset != 0 {
			return record{}, ErrInvalidRecord
		}
	case 0x02, 0x04: // 扩展段地址 / 扩展线性地址：LL=2，AAAA=0
		if rec.length != 2 || rec.offset != 0 {
			return record{}, ErrInvalidRecord
		}
	case 0x03, 0x05: // 起始地址：LL=4，AAAA=0
		if rec.length != 4 || rec.offset != 0 {
			return record{}, ErrInvalidRecord
		}
	default:
		return record{}, ErrUnknownType
	}

	return rec, nil
}

// hexNibble 将一个十六进制字符（大小写均可）转换为半字节值。
func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// checkDataRange 校验数据记录的 AAAA+LL 是否越过 64KiB 边界。
// 恰为 0x10000 允许（区间末端刚好落在边界上），0x10001 拒绝。
func checkDataRange(rec record) error {
	if int(rec.offset)+int(rec.length) > 0x10000 {
		return ErrOutOfRange
	}
	return nil
}
