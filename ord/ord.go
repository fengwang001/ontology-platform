// Package ord 提供显式字节序的整数读写与字节反转。不依赖其他包。
package ord

import (
	"encoding/binary"
	"math/bits"
)

// ByteOrder 只有大端与小端两种。
type ByteOrder bool

const (
	BigEndian    ByteOrder = true
	LittleEndian ByteOrder = false
)

func (o ByteOrder) bo() binary.ByteOrder {
	if o == BigEndian {
		return binary.BigEndian
	}
	return binary.LittleEndian
}

func (o ByteOrder) PutUint16(b []byte, v uint16) { o.bo().PutUint16(b, v) }
func (o ByteOrder) PutUint32(b []byte, v uint32) { o.bo().PutUint32(b, v) }
func (o ByteOrder) PutUint64(b []byte, v uint64) { o.bo().PutUint64(b, v) }

func (o ByteOrder) Uint16(b []byte) uint16 { return o.bo().Uint16(b) }
func (o ByteOrder) Uint32(b []byte) uint32 { return o.bo().Uint32(b) }
func (o ByteOrder) Uint64(b []byte) uint64 { return o.bo().Uint64(b) }

func (o ByteOrder) PutInt16(b []byte, v int16) { o.PutUint16(b, uint16(v)) }
func (o ByteOrder) PutInt32(b []byte, v int32) { o.PutUint32(b, uint32(v)) }
func (o ByteOrder) PutInt64(b []byte, v int64) { o.PutUint64(b, uint64(v)) }

// 有符号读回做符号扩展（int16/int32 转 int 时 Go 自动符号扩展）。
func (o ByteOrder) Int16(b []byte) int16 { return int16(o.Uint16(b)) }
func (o ByteOrder) Int32(b []byte) int32 { return int32(o.Uint32(b)) }
func (o ByteOrder) Int64(b []byte) int64 { return int64(o.Uint64(b)) }

// Swap 按字节反转，大端↔小端互转。
func Swap16(v uint16) uint16 { return bits.ReverseBytes16(v) }
func Swap32(v uint32) uint32 { return bits.ReverseBytes32(v) }
func Swap64(v uint64) uint64 { return bits.ReverseBytes64(v) }
