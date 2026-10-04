// Package dest 实现按外部版本写入、保留删除墓碑的目标索引。
package dest

import "errors"

var (
	ErrInvalid = errors.New("dest: invalid argument")
)

// Record 是一条目标记录（存活或墓碑）。
type Record struct {
	Ver      int64
	Alive    bool
	Incompat bool
	Body     string
}

// Dest 是目标索引。
type Dest struct {
	limit    int
	records  map[string]Record
	conflict int64
}

// New 创建 body 上限为 L 字节的目标索引；L 必须在 1..65536。
func New(L int) (*Dest, error) {
	if L < 1 || L > 65536 {
		return nil, ErrInvalid
	}
	return &Dest{limit: L, records: map[string]Record{}}, nil
}

// Index 按外部版本写入；返回 applied 与 incompat。
func (d *Dest) Index(id, body string, ver int64) (applied, incompat bool) {
	if len(id) < 1 || len(id) > 512 || ver < 1 {
		return false, false
	}
	cur, exists := d.records[id]
	if exists && ver <= cur.Ver {
		d.conflict++
		return false, false
	}
	if len(body) > d.limit {
		d.records[id] = Record{Ver: ver, Alive: false, Incompat: true}
		return true, true
	}
	d.records[id] = Record{Ver: ver, Alive: true, Body: body}
	return true, false
}

// Delete 按外部版本删除并留下墓碑。
func (d *Dest) Delete(id string, ver int64) (applied bool) {
	if len(id) < 1 || len(id) > 512 || ver < 1 {
		return false
	}
	cur, exists := d.records[id]
	if exists && ver <= cur.Ver {
		d.conflict++
		return false
	}
	d.records[id] = Record{Ver: ver, Alive: false}
	return true
}

// Conflicts 返回累计版本冲突数。
func (d *Dest) Conflicts() int64 { return d.conflict }

// Failed 返回当前带不兼容标记墓碑的 id 集合（拷贝）。
func (d *Dest) Failed() map[string]struct{} {
	out := map[string]struct{}{}
	for id, r := range d.records {
		if r.Incompat {
			out[id] = struct{}{}
		}
	}
	return out
}

// Records 返回全部记录（拷贝，测试/校验用）。
func (d *Dest) Records() map[string]Record {
	out := make(map[string]Record, len(d.records))
	for id, r := range d.records {
		out[id] = r
	}
	return out
}

// PurgeTombstones 清除所有墓碑（存活记录保留）。
func (d *Dest) PurgeTombstones() {
	for id, r := range d.records {
		if !r.Alive {
			delete(d.records, id)
		}
	}
}

// Clear 清空全部记录与计数。
func (d *Dest) Clear() {
	d.records = map[string]Record{}
	d.conflict = 0
}
