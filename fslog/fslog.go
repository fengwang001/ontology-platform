// Package fslog 实现前向安全的密钥演进审计日志及其检查点校验器。
//
// 写入者只保存当前密钥，每写入一条数据或封存记录即调用一次 evolve 丢弃旧密钥，
// 写入换钥记录则调用一次 rekey。校验者持有若干 (序号, 密钥) 检查点，
// 从最小序号检查点出发一趟校验整段日志。
package fslog

import (
	"errors"
	"strconv"
	"sync"
)

const (
	// MaxTs 是允许的最大时间戳。
	MaxTs uint64 = 1_000_000_000_000_000
	// MinCap 是允许的最小容量（含封存记录）。
	MinCap uint64 = 2
	// MaxCap 是允许的最大容量（含封存记录）。
	MaxCap uint64 = 1_000_000
	// MaxDataLimit 是单条数据上限的最大值。
	MaxDataLimit uint64 = 4096
)

// 条目类型。
const (
	TypData  uint8 = 0 // 数据记录
	TypSeal  uint8 = 1 // 封存记录
	TypRekey uint8 = 2 // 换钥记录
)

var (
	ErrInvalidConfig  = errors.New("fslog: 配置非法")
	ErrInvalidParam   = errors.New("fslog: 参数非法")
	ErrSealed         = errors.New("fslog: 日志已封存")
	ErrTimeRegression = errors.New("fslog: 时间回退")
	ErrCapacityFull   = errors.New("fslog: 容量已满")
)

// Funcs 是构造时注入的三个确定性函数。
type Funcs struct {
	// Evolve 由当前密钥推出下一条密钥。
	Evolve func(k uint64) uint64
	// Rekey 由当前密钥与换钥条目序号 i 推出下一条密钥。
	Rekey func(k uint64, i uint64) uint64
	// Mac 计算第 i 条、类型为 typ、时间戳为 ts、数据为 data 的条目标记。
	Mac func(k, i uint64, typ uint8, ts uint64, data []byte) uint64
}

// Config 是日志的构造参数。
type Config struct {
	Funcs
	// K0 是初始密钥。
	K0 uint64
	// Cap 是含封存记录在内的总条数上限，取值 [2, 10^6]。
	Cap uint64
	// MaxData 是单条数据上限（字节），取值 [0, 4096]。
	MaxData uint64
}

func (c Config) validate() error {
	if c.Evolve == nil || c.Rekey == nil || c.Mac == nil {
		return ErrInvalidConfig
	}
	if c.Cap < MinCap || c.Cap > MaxCap || c.MaxData > MaxDataLimit {
		return ErrInvalidConfig
	}
	return nil
}

// Entry 是一条日志条目。
type Entry struct {
	Index uint64
	Typ   uint8
	Ts    uint64
	Data  []byte
	Tag   uint64
}

// Log 是前向安全的密钥演进审计日志。所有方法可并发调用，
// 结果等价于某个串行顺序。写入者任意时刻只保留一个当前密钥。
type Log struct {
	mu      sync.Mutex
	cfg     Config
	entries []Entry
	k       uint64 // 当前密钥 ki
	i       uint64 // 下一条序号
	lastTs  uint64
	sealed  bool
}

// NewLog 构造日志。配置非法时整体拒绝。
func NewLog(cfg Config) (*Log, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Log{cfg: cfg, k: cfg.K0}, nil
}

// Append 追加一条数据记录。拒绝次序：参数非法、已封存、时间回退、容量已满。
// Append 要求写入后总条数仍不超过 cap-1，为封存记录预留一个位置。
func (l *Log) Append(ts uint64, data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ts > MaxTs || uint64(len(data)) > l.cfg.MaxData {
		return ErrInvalidParam
	}
	if l.sealed {
		return ErrSealed
	}
	if ts < l.lastTs {
		return ErrTimeRegression
	}
	if l.i >= l.cfg.Cap-1 {
		return ErrCapacityFull
	}
	l.appendLocked(TypData, ts, data)
	l.k = l.cfg.Evolve(l.k)
	return nil
}

// Seal 追加封存记录并封存日志，此后不再接受写入与导出。
// 拒绝次序：参数非法、已封存、时间回退。Seal 不受容量预留限制。
func (l *Log) Seal(ts uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ts > MaxTs {
		return ErrInvalidParam
	}
	if l.sealed {
		return ErrSealed
	}
	if ts < l.lastTs {
		return ErrTimeRegression
	}
	l.appendLocked(TypSeal, ts, []byte(strconv.FormatUint(l.i, 10)))
	l.k = l.cfg.Evolve(l.k)
	l.sealed = true
	return nil
}

// Rekey 追加换钥记录，随后密钥变为 rekey(k, i)（不调用 evolve）。
// 拒绝次序与容量规则同 Append，换钥记录同样占一条容量，且不置封存。
func (l *Log) Rekey(ts uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ts > MaxTs {
		return ErrInvalidParam
	}
	if l.sealed {
		return ErrSealed
	}
	if ts < l.lastTs {
		return ErrTimeRegression
	}
	if l.i >= l.cfg.Cap-1 {
		return ErrCapacityFull
	}
	idx := l.i
	l.appendLocked(TypRekey, ts, []byte(strconv.FormatUint(idx, 10)))
	l.k = l.cfg.Rekey(l.k, idx)
	return nil
}

// appendLocked 追加条目并推进序号与 lastTs，不改动当前密钥。
func (l *Log) appendLocked(typ uint8, ts uint64, data []byte) {
	d := append([]byte(nil), data...)
	l.entries = append(l.entries, Entry{
		Index: l.i,
		Typ:   typ,
		Ts:    ts,
		Data:  d,
		Tag:   l.cfg.Mac(l.k, l.i, typ, ts, d),
	})
	l.i++
	l.lastTs = ts
}

// Export 返回当前 (i, ki) 供校验者托管为检查点。已封存后不可导出。
func (l *Log) Export() (i uint64, k uint64, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sealed {
		return 0, 0, ErrSealed
	}
	return l.i, l.k, nil
}

// Entries 返回当前条目列表的拷贝（某一时刻的一致快照）。
func (l *Log) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, len(l.entries))
	for idx, e := range l.entries {
		out[idx] = e
		out[idx].Data = append([]byte(nil), e.Data...)
	}
	return out
}

// SelfVerify 对日志自身条目的快照做检查点校验。
func (l *Log) SelfVerify(checkpoints map[uint64]uint64) Report {
	return Verify(l.cfg.Funcs, l.Entries(), checkpoints)
}
