package cert

import "errors"

// State 是证书生命周期状态。
type State int

const (
	Pending State = iota
	Active
	Retiring
	Revoked
	Retired
	Lapsed
)

// Cert 是不可变证书。
type Cert struct {
	Serial string
	Dev    string
	Nb     int64
	Na     int64
}

// MaxTime 是允许的时间上界（秒）。
const MaxTime int64 = 1_000_000_000_000

func (s State) String() string { return stateNames[s] }
func (s State) Terminal() bool { return s == Revoked || s == Retired || s == Lapsed }

var stateNames = [...]string{"Pending", "Active", "Retiring", "Revoked", "Retired", "Lapsed"}

// 全量哨兵错误；rotate 与 conn 通过别名复用，保证判定基于同一错误实例。
var (
	ErrInvalid       = errors.New("cert: invalid argument")
	ErrClockBack     = errors.New("cert: clock moved backwards")
	ErrDupSerial     = errors.New("cert: duplicate serial")
	ErrPendingExists = errors.New("cert: pending certificate already exists")
	ErrTooEarly      = errors.New("cert: not within renewal window")
	ErrUnknown       = errors.New("cert: unknown serial")
	ErrMismatch      = errors.New("cert: serial does not belong to device")
	ErrFinal         = errors.New("cert: certificate already in final state")
	ErrRevoked       = errors.New("cert: certificate revoked")
	ErrRetired       = errors.New("cert: certificate retired")
	ErrLapsed        = errors.New("cert: certificate lapsed")
	ErrNotYet        = errors.New("cert: certificate not yet valid")
	ErrExpired       = errors.New("cert: certificate expired")
	ErrNoSession     = errors.New("cert: no active session for device")
)

// ValidConfig 校验构造参数：三者均在 [1, 1e9]。
func ValidConfig(grace, pendingTTL, renewWindow int64) bool {
	return 1 <= grace && grace <= 1e9 &&
		1 <= pendingTTL && pendingTTL <= 1e9 &&
		1 <= renewWindow && renewWindow <= 1e9
}

// ValidCert 校验证书字段：序列号/设备非空、0 <= nb < na <= 1e12。
func (c Cert) Valid() bool {
	return c.Serial != "" && c.Dev != "" && 0 <= c.Nb && c.Nb < c.Na && c.Na <= MaxTime
}

// ValidTime 判断时刻是否落在 [0, 1e12]。
func ValidTime(now int64) bool { return 0 <= now && now <= MaxTime }

// LapseAt 计算待确认截止：max(now, nb) + ttl。
func LapseAt(nb, now, ttl int64) int64 {
	start := now
	if nb > start {
		start = nb
	}
	return start + ttl
}

// RetireAt 计算退役时刻：min(now+grace, na)。
func RetireAt(na, now, grace int64) int64 {
	at := now + grace
	if na < at {
		at = na
	}
	return at
}

// WithinRenew 判断 now 是否已进入续期窗口：now >= na-window（过期也允许）。
func WithinRenew(na, now, window int64) bool { return now >= na-window }
