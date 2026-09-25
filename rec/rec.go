// Package rec 定义单条记录、校验与版本比较。不依赖其他包。
package rec

import "errors"

// 可判定哨兵错误，三者互不相同。
var (
	ErrKey = errors.New("rec: empty key")
	ErrVer = errors.New("rec: version not positive or not strictly increasing")
	ErrVal = errors.New("rec: empty value for live record")
)

// Record 是一条副本记录：活值（Del=false, Val 非空）或墓碑（Del=true, Val 为空）。
type Record struct {
	Val string
	Ver int64
	Del bool
}

// Live 构造活值记录并校验。
func Live(key, val string, ver int64) (Record, error) {
	if key == "" {
		return Record{}, ErrKey
	}
	if val == "" {
		return Record{}, ErrVal
	}
	return Record{Val: val, Ver: ver}, nil
}

// Tomb 构造墓碑记录并校验（Val 必须为空串）。
func Tomb(key string, ver int64) (Record, error) {
	if key == "" {
		return Record{}, ErrKey
	}
	return Record{Ver: ver, Del: true}, nil
}

// CheckVer 校验版本严格递增：必须为正且大于当前全局最大版本。
func CheckVer(ver, max int64) error {
	if ver <= 0 || ver <= max {
		return ErrVer
	}
	return nil
}

// Tomb 判定是否墓碑。
func (r Record) Tomb() bool { return r.Del }

// VerOf 取记录版本；键从未写过（ok=false）视作版本 0，比任何真实版本都小。
func VerOf(r Record, ok bool) int64 {
	if !ok {
		return 0
	}
	return r.Ver
}
