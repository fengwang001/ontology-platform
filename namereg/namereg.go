// Package namereg 维护每个游戏服内的名字登记与离开后的名字保留。
package namereg

import "errors"

// 哨兵错误。
var (
	ErrInvalidParam = errors.New("namereg: invalid parameter")
	ErrOccupied     = errors.New("namereg: name is occupied")
	ErrReserved     = errors.New("namereg: name is reserved by another character")
)

// nameRec 是某服某名字的唯一记录：持有者与保留者。
type nameRec struct {
	holder   string // 当前持有者；空表示无人持有
	reserver string // 保留者；空表示无保留
	expire   int64  // 保留到期时刻（毫秒）；now < expire 才算保留中
}

// Registry 是全部服的名字表。
type Registry struct {
	shards map[string]map[string]*nameRec

	// probes 统计一次名字判定触及的记录字段/映射数。非导出，仅用于验证。
	probes int
}

// NewRegistry 创建空名字表。
func NewRegistry() *Registry {
	return &Registry{shards: map[string]map[string]*nameRec{}}
}

// InitShard 为新服建立名字分区。
func (r *Registry) InitShard(id string) {
	if id == "" {
		return
	}
	if _, ok := r.shards[id]; !ok {
		r.shards[id] = map[string]*nameRec{}
	}
}

// resetProbes / probeCount 供同包测试使用。
func (r *Registry) resetProbes()    { r.probes = 0 }
func (r *Registry) probeCount() int { return r.probes }

func (r *Registry) tick(n int) { r.probes += n }

// rec 取出某服某名字的唯一记录（不存在则惰性建立）。
func (r *Registry) rec(shardID, name string) *nameRec {
	r.tick(1) // 外层 map 查询
	m := r.shards[shardID]
	if m == nil {
		m = map[string]*nameRec{}
		r.shards[shardID] = m
	}
	r.tick(1) // 内层 map 查询
	rec := m[name]
	if rec == nil {
		rec = &nameRec{}
		m[name] = rec
	}
	return rec
}

// reservedActive 为纯读判定：now < expire 才算保留中，取等即释放；不修改记录。
func (rec *nameRec) reservedActive(now int64, r *Registry) bool {
	r.tick(1) // 读取到期字段一次
	return rec.reserver != "" && now < rec.expire
}

// check 执行“占用 + 保留”判定。char 为申请者，纯读不改记录。
// 名字被本人持有时直接放行；其余情况下：他人持有=占用；他人有效保留=保留中。
func (rec *nameRec) check(now int64, char string, r *Registry) error {
	switch {
	case rec.holder != "" && rec.holder != char:
		return ErrOccupied
	case rec.reserver != char && rec.reservedActive(now, r):
		return ErrReserved
	default:
		return nil
	}
}

// Hold 查询并登记：在 shard 上让 char 持有 name。
func (r *Registry) Hold(now int64, shardID, char, name string) error {
	if shardID == "" || char == "" || name == "" {
		return ErrInvalidParam
	}
	rec := r.rec(shardID, name)
	if err := rec.check(now, char, r); err != nil {
		return err
	}
	rec.holder = char
	// 登记成功才清除已过期或本人的保留；被拒绝的判定不触碰记录。
	rec.reserver = ""
	rec.expire = 0
	return nil
}

// Check 是纯判定：名字被占用返回 ErrOccupied，被他人有效保留返回 ErrReserved。
// 不修改任何记录，供编排层在操作接受前做拒绝判定。
func (r *Registry) Check(now int64, shardID, char, name string) error {
	if shardID == "" || char == "" || name == "" {
		return ErrInvalidParam
	}
	return r.rec(shardID, name).check(now, char, r)
}

// Release 立即释放 char 在 shard 持有的 name，不产生保留。
func (r *Registry) Release(shardID, char, name string) {
	if shardID == "" || char == "" || name == "" {
		return
	}
	m := r.shards[shardID]
	if m == nil {
		return
	}
	rec := m[name]
	if rec == nil || rec.holder != char {
		return
	}
	rec.holder = ""
}

// Detain 将 name 在 shard 上置为 char 的保留，到期 now+retain。
func (r *Registry) Detain(now int64, retain int64, shardID, char, name string) {
	if shardID == "" || char == "" || name == "" || retain < 0 {
		return
	}
	rec := r.rec(shardID, name)
	rec.holder = ""
	rec.reserver = char
	rec.expire = now + retain
}

// ---- 观测与探针入口（非业务状态变更）----

// Holder 返回某服某名字的当前持有者（无人持有返回空串）。
func (r *Registry) Holder(shardID, name string) string {
	m := r.shards[shardID]
	if m == nil {
		return ""
	}
	rec := m[name]
	if rec == nil {
		return ""
	}
	return rec.holder
}

// Reserver 返回保留者与其到期时刻；调用方按 now < expire 判定是否有效。
func (r *Registry) Reserver(shardID, name string) (string, int64) {
	m := r.shards[shardID]
	if m == nil {
		return "", 0
	}
	rec := m[name]
	if rec == nil {
		return "", 0
	}
	return rec.reserver, rec.expire
}

// ProbeCount 返回自 ResetProbes 以来名字判定触及的记录数。
func (r *Registry) ProbeCount() int { return r.probes }

// ResetProbes 清零探针。
func (r *Registry) ResetProbes() { r.probes = 0 }

// EachName 遍历某服全部名字记录。
func (r *Registry) EachName(shardID string, fn func(name, holder, reserver string, expire int64)) {
	for name, rec := range r.shards[shardID] {
		fn(name, rec.holder, rec.reserver, rec.expire)
	}
}
