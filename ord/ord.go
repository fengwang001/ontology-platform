// Package ord 按显式声明的字节序读写 16/32/64 位整数，并提供字节反转。
// 不依赖其他包。
package ord

// ByteOrder 只有两种：大端（最高有效字节在前）与小端（最低有效字节在前）。
type ByteOrder int

const (
	BigEndian ByteOrder = iota
	LittleEndian
)

func PutUint16(o ByteOrder, b []byte, v uint16) {
	if o == BigEndian {
		b[0], b[1] = byte(v>>8), byte(v)
	} else {
		b[0], b[1] = byte(v), byte(v>>8)
	}
}

func Uint16(o ByteOrder, b []byte) uint16 {
	if o == BigEndian {
		return uint16(b[0])<<8 | uint16(b[1])
	}
	return uint16(b[1])<<8 | uint16(b[0])
}

func PutUint32(o ByteOrder, b []byte, v uint32) {
	if o == BigEndian {
		b[0], b[1], b[2], b[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	} else {
		b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
	}
}

func Uint32(o ByteOrder, b []byte) uint32 {
	if o == BigEndian {
		return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	}
	return uint32(b[3])<<24 | uint32(b[2])<<16 | uint32(b[1])<<8 | uint32(b[0])
}

func PutUint64(o ByteOrder, b []byte, v uint64) {
	if o == BigEndian {
		for i := 0; i < 8; i++ {
			b[i] = byte(v >> (56 - 8*i))
		}
	} else {
		for i := 0; i < 8; i++ {
			b[i] = byte(v >> (8 * i))
		}
	}
}

func Uint64(o ByteOrder, b []byte) uint64 {
	var v uint64
	if o == BigEndian {
		for i := 0; i < 8; i++ {
			v |= uint64(b[i]) << (56 - 8*i)
		}
	} else {
		for i := 0; i < 8; i++ {
			v |= uint64(b[i]) << (8 * i)
		}
	}
	return v
}

// 有符号写：与无符号相同的位模式；有符号读：按位模式 reinterpret，即符号扩展。

func PutInt16(o ByteOrder, b []byte, v int16) { PutUint16(o, b, uint16(v)) }
func Int16(o ByteOrder, b []byte) int16       { return int16(Uint16(o, b)) }
func PutInt32(o ByteOrder, b []byte, v int32) { PutUint32(o, b, uint32(v)) }
func Int32(o ByteOrder, b []byte) int32       { return int32(Uint32(o, b)) }
func PutInt64(o ByteOrder, b []byte, v int64) { PutUint64(o, b, uint64(v)) }
func Int64(o ByteOrder, b []byte) int64       { return int64(Uint64(o, b)) }

// Swap 把 2/4/8 字节的值按字节反转（大端↔小端互转）。

func Swap16(v uint16) uint16 { return v<<8 | v>>8 }

func Swap32(v uint32) uint32 {
	return v<<24 | (v&0x0000FF00)<<8 | (v&0x00FF0000)>>8 | v>>24
}

func Swap64(v uint64) uint64 {
	return v<<56 | (v&0x000000000000FF00)<<40 | (v&0x0000000000FF0000)<<24 |
		(v&0x00000000FF000000)<<8 | (v&0x000000FF00000000)>>8 |
		(v&0x0000FF0000000000)>>24 | (v&0x00FF000000000000)>>40 | v>>56
}
