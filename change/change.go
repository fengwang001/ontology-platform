// Package change 定义基表变更记录及其二进制编解码。
package change

import (
	"bytes"
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

func (o Op) String() string {
	switch o {
	case Insert:
		return "insert"
	case Delete:
		return "delete"
	case Update:
		return "update"
	default:
		return "unknown"
	}
}

// Record 是一条基表记录。GroupMissing 为真时表示分组缺失（区别于空串）。
type Record struct {
	ID           string
	Group        string
	GroupMissing bool
	Value        float64
}

// Change 是一条变更。Update 时 OldGroup 为记录原分组。
type Change struct {
	Op       Op
	Rec      Record
	OldGroup string
	Ver      uint64
}

var (
	// ErrMalformed 表示记录体编码不合法。
	ErrMalformed = errors.New("change: malformed record")
	// ErrMissingGroup 表示变更缺少必填分组键。
	ErrMissingGroup = errors.New("change: missing group key")
	// ErrNaN 表示变更值为 NaN。
	ErrNaN = errors.New("change: NaN value")
)

const (
	flagGroupMissing = 1 << 0
	flagHasOldGroup  = 1 << 1
	flagNegZero      = 1 << 2
)

// Validate 校验变更：分组必须存在（空串合法），值不得为 NaN。
func (c Change) Validate() error {
	if c.Rec.GroupMissing {
		return ErrMissingGroup
	}
	if math.IsNaN(c.Rec.Value) {
		return ErrNaN
	}
	return nil
}

func putStr(b *bytes.Buffer, s string) {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(s)))
	b.Write(l[:])
	b.WriteString(s)
}

func getStr(p []byte, off int) (string, int, error) {
	if len(p)-off < 4 {
		return "", 0, ErrMalformed
	}
	n := int(binary.BigEndian.Uint32(p[off:]))
	off += 4
	if len(p)-off < n {
		return "", 0, ErrMalformed
	}
	s := string(p[off : off+n])
	return s, off + n, nil
}

// Encode 将变更序列化为自描述记录体。
func (c Change) Encode() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var flags byte
	if c.Rec.GroupMissing {
		flags |= flagGroupMissing
	}
	if c.Op == Update {
		flags |= flagHasOldGroup
	}
	v := c.Rec.Value
	if math.Signbit(v) && v == 0 {
		flags |= flagNegZero // 解码后归一化为 +0
	}
	buf := bytes.NewBuffer(make([]byte, 0, 40))
	buf.WriteByte(byte(c.Op))
	buf.WriteByte(flags)
	var ver [8]byte
	binary.BigEndian.PutUint64(ver[:], c.Ver)
	buf.Write(ver[:])
	var fv [8]byte
	binary.BigEndian.PutUint64(fv[:], math.Float64bits(v))
	buf.Write(fv[:])
	putStr(buf, c.Rec.ID)
	putStr(buf, c.Rec.Group)
	if c.Op == Update {
		putStr(buf, c.OldGroup)
	}
	return buf.Bytes(), nil
}

// Decode 从记录体还原变更。
func Decode(p []byte) (Change, error) {
	if len(p) < 18 {
		return Change{}, ErrMalformed
	}
	c := Change{Op: Op(p[0])}
	flags := p[1]
	if c.Op < Insert || c.Op > Update {
		return Change{}, ErrMalformed
	}
	c.Ver = binary.BigEndian.Uint64(p[2:10])
	bits := binary.BigEndian.Uint64(p[10:18])
	c.Rec.Value = math.Float64frombits(bits)
	if flags&flagNegZero != 0 {
		c.Rec.Value = 0 // +0/-0 归一化
	}
	var err error
	off := 18
	if c.Rec.ID, off, err = getStr(p, off); err != nil {
		return Change{}, err
	}
	if c.Rec.Group, off, err = getStr(p, off); err != nil {
		return Change{}, err
	}
	if flags&flagHasOldGroup != 0 {
		if c.Op != Update {
			return Change{}, ErrMalformed
		}
		if c.OldGroup, off, err = getStr(p, off); err != nil {
			return Change{}, err
		}
	} else if c.Op == Update {
		return Change{}, ErrMalformed
	}
	if off != len(p) {
		return Change{}, ErrMalformed
	}
	c.Rec.GroupMissing = flags&flagGroupMissing != 0
	if err := c.Validate(); err != nil {
		return Change{}, err
	}
	return c, nil
}

// EqualBytes 判断两条变更编码后是否逐字节相同（幂等判定依据）。
func (c Change) EqualBytes(other Change) bool {
	a, err1 := c.Encode()
	b, err2 := other.Encode()
	return err1 == nil && err2 == nil && bytes.Equal(a, b)
}
