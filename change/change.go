package change

import (
	"encoding/binary"
	"errors"
	"math"
)

// Op 是变更操作类型。
type Op uint8

const (
	Insert Op = 1
	Delete Op = 2
	Update Op = 3
)

// Change 描述一次基表变更。GroupSet=false 表示分组缺失（区别于空串分组）。
// Update 时 Value 为新值、NewGroup/NewValue 为目标；Insert/Delete 用 Group/Value。
type Change struct {
	Op       Op
	Version  int64
	Group    string
	GroupSet bool
	Value    float64

	NewGroup string
	NewValue float64
}

// ErrMalformed 表示记录体编码不合法。
var ErrMalformed = errors.New("change: malformed record")

// Encode 将变更序列化为自描述记录体（不含长度前缀与 CRC）。
func (c Change) Encode() []byte {
	g := []byte(c.Group)
	ng := []byte(c.NewGroup)
	b := make([]byte, 0, 30+len(g)+len(ng))
	b = append(b, byte(c.Op))
	b = binary.BigEndian.AppendUint64(b, uint64(c.Version))

	var flags byte
	if c.GroupSet {
		flags |= 1
	}
	b = append(b, flags)

	b = binary.BigEndian.AppendUint64(b, math.Float64bits(normZero(c.Value)))
	b = binary.BigEndian.AppendUint32(b, uint32(len(g)))
	b = append(b, g...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(ng)))
	b = append(b, ng...)
	b = binary.BigEndian.AppendUint64(b, math.Float64bits(normZero(c.NewValue)))
	return b
}

// Decode 解析 Encode 产生的记录体。
func Decode(b []byte) (Change, error) {
	var c Change
	pos := 0
	take := func(n int) ([]byte, error) {
		if pos+n > len(b) {
			return nil, ErrMalformed
		}
		s := b[pos : pos+n]
		pos += n
		return s, nil
	}

	op, err := take(1)
	if err != nil {
		return c, err
	}
	c.Op = Op(op[0])

	v, err := take(8)
	if err != nil {
		return c, err
	}
	c.Version = int64(binary.BigEndian.Uint64(v))

	fl, err := take(1)
	if err != nil {
		return c, err
	}
	c.GroupSet = fl[0]&1 != 0

	val, err := take(8)
	if err != nil {
		return c, err
	}
	c.Value = math.Float64frombits(binary.BigEndian.Uint64(val))

	glb, err := take(4)
	if err != nil {
		return c, err
	}
	gl := int(binary.BigEndian.Uint32(glb))
	gb, err := take(gl)
	if err != nil {
		return c, err
	}
	c.Group = string(gb)

	nlb, err := take(4)
	if err != nil {
		return c, err
	}
	nl := int(binary.BigEndian.Uint32(nlb))
	nb, err := take(nl)
	if err != nil {
		return c, err
	}
	c.NewGroup = string(nb)

	nvb, err := take(8)
	if err != nil {
		return c, err
	}
	c.NewValue = math.Float64frombits(binary.BigEndian.Uint64(nvb))
	if pos != len(b) {
		return c, ErrMalformed
	}
	return c, nil
}

// normZero 把 -0 归一成 +0，使正负零在编码/比较层面相等。
func normZero(f float64) float64 {
	if f == 0 {
		return 0
	}
	return f
}
