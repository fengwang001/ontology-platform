package indexstore

import (
	"sort"
	"strconv"
	"strings"
)

// table 是按主键保存行的主表。内存映射随每次同步提交更新；启动时从磁盘
// 重建（磁盘 table:* 与 WAL 末尾一致）。
//
// secOwners 为主表侧维护的“非空二级键 -> 主键”最新映射，与主表严格
// 同步。唯一性判定基于它完成，O(1)，与索引落后多少条日志无关。
type table struct {
	disk      *Disk
	rows      map[string]Row
	secOwners map[string]string
}

func newTable(d *Disk) *table {
	return &table{disk: d, rows: map[string]Row{}, secOwners: map[string]string{}}
}

// load 从磁盘重建主表与二级键最新属主映射（骨架）。
func (t *table) load() error {
	for k, v := range t.disk.files {
		if !strings.HasPrefix(k, "table:") {
			continue
		}
		pk := k[len("table:"):]
		row, err := decodeRow(pk, v)
		if err != nil {
			return err
		}
		t.rows[pk] = row
		if row.HasSec {
			t.secOwners[row.Sec] = pk
		}
	}
	return nil
}

// snapshot 返回主表当前全部行（自检与朴素对照用）。
func (t *table) snapshot() []Row {
	out := make([]Row, 0, len(t.rows))
	for _, r := range t.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PK < out[j].PK })
	return out
}

// ownerOf 返回非空二级键的最新属主（主表视角）；O(1)。
func (t *table) ownerOf(sec string) (string, bool) {
	pk, ok := t.secOwners[sec]
	return pk, ok
}

func (t *table) get(pk string) (Row, bool) {
	r, ok := t.rows[pk]
	return r, ok
}

// commitWrite 与 WAL 追加在同一个磁盘提交步骤内更新主表；提交成功后
// 更新内存状态。唯一性/存在性判定由调用方在调用前完成。
func (t *table) commitWrite(w *wal, step string, e LogEntry) bool {
	changes := map[string]string{}
	var deletes []string
	key := "table:" + e.PK
	if e.Op == LogDelete {
		deletes = append(deletes, key)
	} else {
		changes[key] = encodeRow(e.NewSec, e.HasNew)
	}
	if !w.appendWith(step, e, changes, deletes) {
		return false
	}
	t.memoryApply(e)
	return true
}

// memoryApply 依据日志更新内存主表（提交成功后调用）。
func (t *table) memoryApply(e LogEntry) {
	if old, ok := t.rows[e.PK]; ok && old.HasSec {
		if cur, owned := t.secOwners[old.Sec]; owned && cur == e.PK {
			delete(t.secOwners, old.Sec)
		}
	}
	if e.Op == LogDelete {
		delete(t.rows, e.PK)
		return
	}
	row := Row{PK: e.PK, Sec: e.NewSec, HasSec: e.HasNew}
	t.rows[e.PK] = row
	if row.HasSec {
		t.secOwners[row.Sec] = e.PK
	}
}

func encodeRow(sec string, hasSec bool) string {
	return sec + fs + strconv.FormatBool(hasSec)
}

func decodeRow(pk, v string) (Row, error) {
	p := strings.SplitN(v, fs, 2)
	if len(p) != 2 {
		return Row{}, errf(ErrLogGap, "corrupt table record for %q", pk)
	}
	has, err := strconv.ParseBool(p[1])
	if err != nil {
		return Row{}, err
	}
	return Row{PK: pk, Sec: p[0], HasSec: has}, nil
}
