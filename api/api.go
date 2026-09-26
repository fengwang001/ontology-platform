// Package api 对外门面：New/Marshal/Unmarshal/SelfCheck。依赖 codec。
package api

import (
	"errors"

	"ontology/codec"
	"ontology/ord"
)

var errSelf = errors.New("api: self check failed")

type API struct{}

func New() *API { return &API{} }

func (a *API) Marshal(o ord.ByteOrder, w int, vals []int64) ([]byte, error) {
	switch w {
	case 2, 4, 8:
		return codec.Encode(o, w, vals), nil
	default:
		return nil, codec.ErrInvalidWidth
	}
}

func (a *API) Unmarshal(o ord.ByteOrder, w int, buf []byte) ([]int64, error) {
	return codec.Decode(o, w, buf)
}

// naive 是教科书式逐字节参照实现，用于核验 ord。
func naivePut(o ord.ByteOrder, w int, b []byte, v uint64) {
	for i := 0; i < w; i++ {
		if o == ord.BigEndian {
			b[i] = byte(v >> (uint(w-1-i) * 8))
		} else {
			b[i] = byte(v >> (uint(i) * 8))
		}
	}
}

func naiveGet(o ord.ByteOrder, w int, b []byte) uint64 {
	var v uint64
	for i := 0; i < w; i++ {
		if o == ord.BigEndian {
			v |= uint64(b[i]) << (uint(w-1-i) * 8)
		} else {
			v |= uint64(b[i]) << (uint(i) * 8)
		}
	}
	return v
}

// SelfCheck 对内置向量核验四条不变量：往返一致、序互逆、与朴素参照一致、失败不留痕。
func (a *API) SelfCheck() error {
	orders := []ord.ByteOrder{ord.BigEndian, ord.LittleEndian}
	vecs := map[int][]uint64{2: {0, 1, 0x8000, 0xFFFF}, 4: {0x01020304, 0xFFFFFFFF}, 8: {1, 0x8000000000000000, 0xFFFFFFFFFFFFFFFF}}
	for _, w := range []int{2, 4, 8} {
		for _, o := range orders {
			for _, v := range vecs[w] {
				got, ref := make([]byte, w), make([]byte, w)
				switch w {
				case 2:
					o.PutUint16(got, uint16(v))
				case 4:
					o.PutUint32(got, uint32(v))
				case 8:
					o.PutUint64(got, v)
				}
				naivePut(o, w, ref, v)
				for i := range got { // 不变量 3：字节级一致
					if got[i] != ref[i] {
						return errSelf
					}
				}
				var back uint64
				switch w {
				case 2:
					back = uint64(o.Uint16(got))
				case 4:
					back = uint64(o.Uint32(got))
				case 8:
					back = o.Uint64(got)
				}
				if back != naiveGet(o, w, got) { // 不变量 3：读回一致
					return errSelf
				}
			}
			svals := map[int][]int64{
				2: {-1, 1, 32767, -32768},
				4: {-1, 1, 1<<31 - 1, -1 << 31},
				8: {-1, 1, 1<<63 - 1, -1 << 63},
			}[w]
			round, err := codec.Decode(o, w, codec.Encode(o, w, svals))
			if err != nil { // 不变量 1：往返一致
				return errSelf
			}
			for i, v := range svals {
				if round[i] != v {
					return errSelf
				}
			}
		}
	}
	if ord.Swap16(ord.Swap16(0xBEEF)) != 0xBEEF || // 不变量 2：序互逆
		ord.Swap64(ord.Swap64(0x0102030405060708)) != 0x0102030405060708 {
		return errSelf
	}
	be := codec.Encode(ord.BigEndian, 4, []int64{0x01020304})
	if uint64(ord.LittleEndian.Uint32(be)) != uint64(ord.Swap32(0x01020304)) {
		return errSelf
	}
	if _, err := codec.Decode(ord.BigEndian, 3, make([]byte, 6)); err != codec.ErrInvalidWidth {
		return errSelf // 不变量 4：失败不留痕
	}
	if _, err := codec.Decode(ord.BigEndian, 4, make([]byte, 6)); err != codec.ErrMisaligned {
		return errSelf
	}
	if _, err := codec.Decode(ord.BigEndian, 4, make([]byte, 4)); err != nil { // 拒绝后仍可用
		return errSelf
	}
	return nil
}
