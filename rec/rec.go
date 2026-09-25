// Package rec 定义单条记录及其校验、版本比较、墓碑判定。不依赖其他包。
package rec

import "errors"

// 可判定的哨兵错误，互不相同。
var (
	ErrBadKey  = errors.New("rec: key is empty")
	ErrBadVal  = errors.New("rec: put value is empty")
	ErrBadVer  = errors.New("rec: version not positive or not strictly increasing")
	ErrBadSide = errors.New("rec: side must be 0 (A) or 1 (B)")
)

// Record 是副本中的一条记录。墓碑：Del=true 且 Val 必须为空串。
type Record struct {
	Val string
	Ver int64
	Del bool
}

// Live 构造一条活值记录。
func Live(val string, ver int64) Record { return Record{Val: val, Ver: ver} }

// Tomb 构造一条墓碑记录。
func Tomb(ver int64) Record { return Record{Ver: ver, Del: true} }

// IsTomb 判定是否为墓碑。
func (r Record) IsTomb() bool { return r.Del }

// IsLive 判定是否为活值（非墓碑且值非空）。
func (r Record) IsLive() bool { return !r.Del && r.Val != "" }

// CheckKey 校验键非空。
func CheckKey(k string) error {
	if k == "" {
		return ErrBadKey
	}
	return nil
}

// CheckVal 校验活值非空。
func CheckVal(v string) error {
	if v == "" {
		return ErrBadVal
	}
	return nil
}

// CheckVer 校验版本为正且严格大于当前全局最大版本。
func CheckVer(ver, maxVer int64) error {
	if ver <= 0 || ver <= maxVer {
		return ErrBadVer
	}
	return nil
}

// CheckSide 校验副本编号合法。
func CheckSide(side int) error {
	if side != 0 && side != 1 {
		return ErrBadSide
	}
	return nil
}

// VerOf 返回某侧记录的版本；ok=false 表示该侧从未写过，视作版本 0。
func VerOf(r Record, ok bool) int64 {
	if !ok {
		return 0
	}
	return r.Ver
}

// Winner 在两条记录间取版本大者；平局取 a（对账中平局意味着两侧本就一致）。
func Winner(a, b Record) Record {
	if b.Ver > a.Ver {
		return b
	}
	return a
}
