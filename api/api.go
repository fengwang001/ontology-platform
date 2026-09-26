// Package api 是对外门面：New/Marshal/Unmarshal/SelfCheck。依赖 codec。
package api

import (
	"errors"

	"ontology/codec"
	"ontology/ord"
)

// Codec 无状态，可并发使用。
type Codec struct{}

func New() *Codec { return &Codec{} }

func (c *Codec) Marshal(order ord.ByteOrder, width int, vals []int64) []byte {
	return codec.Encode(order, width, vals)
}

func (c *Codec) Unmarshal(order ord.ByteOrder, width int, buf []byte) ([]int64, error) {
	return codec.Decode(order, width, buf)
}

var orders = []ord.ByteOrder{ord.BigEndian, ord.LittleEndian}

// SelfCheck 对一组内置向量核验四条不变量，全部通过返回 nil。
func (c *Codec) SelfCheck() error {
	// 不变量 1：往返一致（各宽度取覆盖边界的有符号值）
	vecs := map[int][]int64{
		2: {0, 1, -1, 32767, -32768},
		4: {0, 1, -1, 2147483647, -2147483648},
		8: {0, 1, -1, 9223372036854775807, -9223372036854775808},
	}
	for _, o := range orders {
		for w, vals := range vecs {
			got, err := codec.Decode(o, w, codec.Encode(o, w, vals))
			if err != nil || len(got) != len(vals) {
				return errors.New("selfcheck: roundtrip decode failed")
			}
			for i := range vals {
				if got[i] != vals[i] {
					return errors.New("selfcheck: roundtrip mismatch")
				}
			}
		}
	}
	// 不变量 2：Swap 互逆，且 BE 写 LE 读 = 字节反转
	var b [8]byte
	for _, v := range []uint64{0, 1, 0x0102030405060708, 0xFFFFFFFFFFFFFFFF} {
		if ord.Swap64(ord.Swap64(v)) != v || ord.Swap32(ord.Swap32(uint32(v))) != uint32(v) ||
			ord.Swap16(ord.Swap16(uint16(v))) != uint16(v) {
			return errors.New("selfcheck: swap not self-inverse")
		}
		ord.PutUint64(ord.BigEndian, b[:], v)
		if ord.Uint64(ord.LittleEndian, b[:]) != ord.Swap64(v) {
			return errors.New("selfcheck: BE/LE not byte-reversed pair")
		}
	}
	// 不变量 3：与朴素参照（逐字节反转）字节级一致
	ord.PutUint32(ord.BigEndian, b[:4], 0x01020304)
	if b[0] != 0x01 || b[1] != 0x02 || b[2] != 0x03 || b[3] != 0x04 {
		return errors.New("selfcheck: BE put disagrees with naive reference")
	}
	ord.PutUint32(ord.LittleEndian, b[:4], 0x01020304)
	if b[0] != 0x04 || b[1] != 0x03 || b[2] != 0x02 || b[3] != 0x01 {
		return errors.New("selfcheck: LE put disagrees with naive reference")
	}
	// 不变量 4：失败不留痕——拒绝即 (nil, error)，之后仍可正常使用
	if got, err := codec.Decode(ord.BigEndian, 3, []byte{1, 2, 3}); got != nil || err == nil {
		return errors.New("selfcheck: invalid width not rejected cleanly")
	}
	if got, err := codec.Decode(ord.BigEndian, 4, []byte{1, 2, 3}); got != nil || err == nil {
		return errors.New("selfcheck: misaligned buffer not rejected cleanly")
	}
	if _, err := codec.Decode(ord.BigEndian, 2, []byte{1, 2}); err != nil {
		return errors.New("selfcheck: unusable after rejection")
	}
	return nil
}
