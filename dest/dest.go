// Package dest 实现按外部版本写入、保留删除墓碑的目标索引。
//
// 每个 id 至多一条记录（ver, 存活或墓碑, 墓碑是否带不兼容标记）。
// ver 严格大于现有版本才应用，否则计一次版本冲突并跳过（不算错误）。
package dest

import (
	"errors"
	"sync"
)

// ErrInvalidParam 表示构造参数 L 不在 1 到 65536 之间。
var ErrInvalidParam = errors.New("dest: invalid parameter")

const (
	// MinBodyLimit 是目标格式允许的最小 body 上限。
	MinBodyLimit = 1
	// MaxBodyLimit 是目标格式允许的最大 body 上限。
	MaxBodyLimit = 65536
)

// Record 描述 dest 中一个 id 的当前记录。
type Record struct {
	Ver          uint64
	Body         []byte
	Tombstone    bool
	Incompatible bool
}

type record struct {
	ver          uint64
	body         []byte
	tombstone    bool
	incompatible bool
}

// Dest 是版本化目标索引，可并发使用。
type Dest struct {
	mu        sync.Mutex
	limit     int
	recs      map[string]record
	conflicts uint64
}

// New 构造目标索引，limit 为目标格式允许的 body 最大字节数。
func New(limit int) (*Dest, error) {
	if limit < MinBodyLimit || limit > MaxBodyLimit {
		return nil, ErrInvalidParam
	}
	return &Dest{limit: limit, recs: make(map[string]record)}, nil
}

// Index 以 ver 写入一条文档，返回 (applied, incompatible)。
//
// 无记录或 ver 严格大于现有 ver 才应用，否则（含 ver 恰等、含现有记录
// 为墓碑）计一次版本冲突并跳过。body 超过 L 属不兼容：若会被应用则改写
// 为带不兼容标记的墓碑（旧值随之消失），否则只计冲突。
func (d *Dest) Index(id string, body []byte, ver uint64) (applied, incompatible bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if rec, ok := d.recs[id]; ok && ver <= rec.ver {
		d.conflicts++
		return false, false
	}
	if len(body) > d.limit {
		d.recs[id] = record{ver: ver, tombstone: true, incompatible: true}
		return true, true
	}
	d.recs[id] = record{ver: ver, body: append([]byte(nil), body...)}
	return true, false
}

// Delete 以 ver 写入墓碑，判定规则与 Index 相同；
// 对无记录的 id 也必须留下墓碑。
func (d *Dest) Delete(id string, ver uint64) (applied bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if rec, ok := d.recs[id]; ok && ver <= rec.ver {
		d.conflicts++
		return false
	}
	d.recs[id] = record{ver: ver, tombstone: true}
	return true
}

// Conflicts 返回累计版本冲突数。
func (d *Dest) Conflicts() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conflicts
}

// Get 返回 id 对应的记录。
func (d *Dest) Get(id string) (Record, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, ok := d.recs[id]
	if !ok {
		return Record{}, false
	}
	return Record{Ver: rec.ver, Body: append([]byte(nil), rec.body...),
		Tombstone: rec.tombstone, Incompatible: rec.incompatible}, true
}

// All 返回全部记录的快照。
func (d *Dest) All() map[string]Record {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]Record, len(d.recs))
	for id, rec := range d.recs {
		out[id] = Record{Ver: rec.ver, Body: append([]byte(nil), rec.body...),
			Tombstone: rec.tombstone, Incompatible: rec.incompatible}
	}
	return out
}

// Empty 报告 dest 是否没有任何记录。
func (d *Dest) Empty() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.recs) == 0
}

// Failures 返回失败集合：当前带不兼容标记墓碑的 id 集合。
func (d *Dest) Failures() map[string]bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]bool)
	for id, rec := range d.recs {
		if rec.tombstone && rec.incompatible {
			out[id] = true
		}
	}
	return out
}

// ClearTombstones 清除全部墓碑（含不兼容墓碑），存活记录保持不变。
func (d *Dest) ClearTombstones() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, rec := range d.recs {
		if rec.tombstone {
			delete(d.recs, id)
		}
	}
}

// Reset 清空全部记录与冲突计数，回到刚构造的状态。
func (d *Dest) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.recs = make(map[string]record)
	d.conflicts = 0
}
