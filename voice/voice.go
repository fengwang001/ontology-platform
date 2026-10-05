// Package voice 维护禁言记录：解除时间与施加者等级快照。
package voice

import "ontology/role"

// Record 为一条禁言记录。L0 是施加瞬间的等级快照，之后不随施加者变化。
type Record struct {
	Until int64
	L0    role.Level
}

// Book 保存所有用户的禁言记录；用户离开房间时记录保留。
type Book struct {
	recs map[string]Record
}

func NewBook() *Book { return &Book{recs: make(map[string]Record)} }

func (b *Book) Clone() *Book {
	c := &Book{recs: make(map[string]Record, len(b.recs))}
	for u, r := range b.recs {
		c.recs[u] = r
	}
	return c
}

// Muted 判断此刻禁言是否生效：now < Until 生效，取等即解除。
func (b *Book) Muted(u string, now int64) bool {
	r, ok := b.recs[u]
	return ok && now < r.Until
}

func (b *Book) Get(u string) (Record, bool) {
	r, ok := b.recs[u]
	return r, ok
}

func (b *Book) Set(u string, until int64, l0 role.Level) {
	b.recs[u] = Record{Until: until, L0: l0}
}

func (b *Book) Unset(u string) { delete(b.recs, u) }

// Records 返回禁言记录副本。
func (b *Book) Records() map[string]Record {
	out := make(map[string]Record, len(b.recs))
	for u, r := range b.recs {
		out[u] = r
	}
	return out
}
