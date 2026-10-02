package aliascache

import (
	"errors"
	"time"
)

// MaxAliasHops 是一条解析链允许经过的别名记录上限，超过即判定为链过长。
const MaxAliasHops = 8

// UpstreamAnswer 是上游对单个名字可能给出的三种应答之一。
// 三种应答互斥：Alias 非空为别名记录；Addresses 非空为地址记录；
// Negative 非空为不存在（否定）应答。
type UpstreamAnswer struct {
	Alias     *AliasRecord
	Addresses *AddressRecord
	Negative  *NegativeRecord
}

type AliasRecord struct {
	Target string
	TTL    time.Duration
}

type AddressRecord struct {
	Addresses []string
	TTL       time.Duration
}

// NegativeRecord 为不存在应答：SOATTL 为授权记录存活时间 S，MinTTL 为最小值 M。
type NegativeRecord struct {
	SOATTL time.Duration
	MinTTL time.Duration
}

type Result struct {
	Name      string
	Aliases   []string
	Addresses []string
	Found     bool
	TTL       time.Duration
}

var (
	ErrEmptyName = errors.New("aliascache: name is empty")
	ErrCycle     = errors.New("aliascache: alias chain contains a cycle")
	ErrTooLong   = errors.New("aliascache: alias chain too long")
)

type UpstreamFunc func(name string) (UpstreamAnswer, error)

type ClockFunc func() time.Time
