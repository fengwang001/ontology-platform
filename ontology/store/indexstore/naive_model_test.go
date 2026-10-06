package indexstore

import (
	"sort"
	"strings"
)

// naiveModel 是独立于实现的朴素参考模型：
//   - 直接用 map 保存主表，并以追加切片保存完整日志；
//   - 唯一二级索引不做增量维护，只在需要时从主表全量重建（O(n)），
//     这正是被实现刻意放弃、仅用于对照的方案；
//   - caughtUp 对应“水位==日志末尾”，追赶前查询一律视为 stale；
//   - 崩溃与重开对主表/日志无影响（同步提交），只把 caughtUp 与
//     崩溃前的水位语义用“落后前缀长度”参数显式重建。
//
// 模型对每种操作独立给出期望输出与判定依据，与实现的 OpLog 逐项对照。
type naiveModel struct {
	rows    map[string]modelRow
	log     []modelEntry
	caught  bool
	ops     []OpRecord
	reopens int
	applied int
	wmLag   int
}

type modelRow struct {
	sec    string
	hasSec bool
	alive  bool
}

type modelEntry struct {
	lsn    int
	op     LogOp
	pk     string
	oldSec string
	hasOld bool
	newSec string
	hasNew bool
}

func newNaiveModel() *naiveModel {
	return &naiveModel{rows: map[string]modelRow{}, caught: true}
}

type modelOutcome struct {
	err    ErrorCode // -1 表示无错误
	lsn    int
	pk     string
	found  bool
	done   bool
	reason string
}

func noErr() modelOutcome { return modelOutcome{err: -1} }

func (m *naiveModel) put(pk, sec string, hasSec bool) modelOutcome {
	if pk == "" {
		m.note(OpRecord{Kind: OpPut, PK: pk, Sec: sec, HasSec: hasSec,
			Reason: "model: pk empty", ErrCode: codeName(ErrInvalidArgument)})
		return modelOutcome{err: ErrInvalidArgument, reason: "pk empty"}
	}
	if hasSec && sec != "" {
		for other, r := range m.rows {
			if r.alive && r.hasSec && r.sec == sec && other != pk {
				m.note(OpRecord{Kind: OpPut, PK: pk, Sec: sec, HasSec: hasSec,
					LSN: len(m.log), Reason: "model: sec owned by " + other,
					ErrCode: codeName(ErrUniqueConflict)})
				return modelOutcome{err: ErrUniqueConflict, reason: "owned by " + other}
			}
		}
	}
	old := m.rows[pk]
	e := modelEntry{
		lsn: len(m.log) + 1, op: LogUpsert, pk: pk,
		oldSec: old.sec, hasOld: old.alive && old.hasSec,
		newSec: sec, hasNew: hasSec,
	}
	m.log = append(m.log, e)
	m.rows[pk] = modelRow{sec: sec, hasSec: hasSec, alive: true}
	m.caught = false // 新写入产生的日志尚未被追赶
	m.note(OpRecord{Kind: OpPut, PK: pk, Sec: sec, HasSec: hasSec, OK: true,
		LSN: e.lsn, Reason: "model: appended lsn"})
	return modelOutcome{err: -1, lsn: e.lsn}
}

func (m *naiveModel) del(pk string) modelOutcome {
	if pk == "" {
		return modelOutcome{err: ErrInvalidArgument, reason: "pk empty"}
	}
	r, ok := m.rows[pk]
	if !ok || !r.alive {
		m.note(OpRecord{Kind: OpDelete, PK: pk, LSN: len(m.log),
			Reason: "model: pk absent", ErrCode: codeName(ErrPrimaryNotFound)})
		return modelOutcome{err: ErrPrimaryNotFound}
	}
	e := modelEntry{lsn: len(m.log) + 1, op: LogDelete, pk: pk,
		oldSec: r.sec, hasOld: r.hasSec}
	m.log = append(m.log, e)
	r.alive = false
	m.rows[pk] = r
	m.caught = false
	m.note(OpRecord{Kind: OpDelete, PK: pk, OK: true, LSN: e.lsn})
	return modelOutcome{err: -1, lsn: e.lsn}
}

func (m *naiveModel) lookup(sec string) modelOutcome {
	if sec == "" {
		return modelOutcome{err: ErrInvalidArgument}
	}
	if !m.caught {
		return modelOutcome{err: ErrIndexStale}
	}
	idx := m.rebuildIndex()
	pk, ok := idx[sec]
	m.note(OpRecord{Kind: OpLookup, Sec: sec, OK: true, Result: pk, Found: ok})
	return modelOutcome{err: -1, pk: pk, found: ok}
}

// catchUp 朴素地从完整日志重放（每条 O(n) 扫描，故意保持直白），
// 但语义上与实现一致：maxBatch<=0 非法；本批窗口序号必须连续；
// 只有追至日志末尾才算 caughtUp。
func (m *naiveModel) catchUp(maxBatch int) modelOutcome {
	if maxBatch <= 0 {
		return modelOutcome{err: ErrInvalidArgument}
	}
	// 模型的“已应用位置”用独立字段跟踪（与 caught 区分）。
	end := m.applied + maxBatch
	if end > len(m.log) {
		end = len(m.log)
	}
	for lsn := m.applied + 1; lsn <= end; lsn++ {
		if m.log[lsn-1].lsn != lsn {
			return modelOutcome{err: ErrLogGap}
		}
		m.applied = lsn
	}
	if m.applied == len(m.log) {
		m.caught = true
	}
	done := m.caught
	m.note(OpRecord{Kind: OpCatchUp, Batch: maxBatch, OK: true,
		Reached: m.applied, LSN: len(m.log),
		Reason: "model: replayed by full rescan, done="})
	return modelOutcome{err: -1, lsn: m.applied, done: done}
}

func (m *naiveModel) verify() (map[string]string, modelOutcome) {
	if !m.caught {
		return nil, modelOutcome{err: ErrIndexStale}
	}
	return m.rebuildIndex(), noErr()
}

// rebuildIndex 从主表当前内容完整重建：这是“正确答案”的唯一来源。
func (m *naiveModel) rebuildIndex() map[string]string {
	idx := map[string]string{}
	pks := make([]string, 0, len(m.rows))
	for pk := range m.rows {
		pks = append(pks, pk)
	}
	sort.Strings(pks)
	for _, pk := range pks {
		r := m.rows[pk]
		if r.alive && r.hasSec && r.sec != "" {
			idx[r.sec] = pk
		}
	}
	return idx
}

// crash 模拟崩溃：主表/日志不变（同步提交），索引与水位被丢弃并按
// lagPrefix 重建——索引恰好含前 (len(log)-lagPrefix) 条日志的效果，
// 水位取 wmLag（wmLag<=lagPrefix，且 wmLag 可为更大的任意落后量）。
func (m *naiveModel) crash(lagPrefix, wmLag int) {
	m.applied = len(m.log) - lagPrefix
	m.caught = false
	m.wmLag = wmLag
	m.reopens++
	m.note(OpRecord{Kind: OpReopen, OK: true,
		Reason: "model crash: applied ahead of wm by prefix replay"})
}

func (m *naiveModel) note(r OpRecord) { m.ops = append(m.ops, r) }

// formatOpLog 把实现与模型的操作日志渲染成逐行文本，便于打印与复现。
func formatOpLog(recs []OpRecord) string {
	var b strings.Builder
	for i, r := range recs {
		b.WriteString(itoa(i + 1))
		b.WriteString(" ")
		b.WriteString(string(r.Kind))
		b.WriteString(" pk=")
		b.WriteString(r.PK)
		if r.Kind == OpPut || r.Kind == OpLookup {
			b.WriteString(" sec=")
			b.WriteString(r.Sec)
			if r.Kind == OpPut {
				b.WriteString(" hasSec=")
				if r.HasSec {
					b.WriteString("true")
				} else {
					b.WriteString("false")
				}
			}
		}
		if r.Kind == OpCatchUp {
			b.WriteString(" batch=")
			b.WriteString(itoa(r.Batch))
			b.WriteString(" reached=")
			b.WriteString(itoa(r.Reached))
		}
		if r.OK {
			b.WriteString(" -> OK")
		}
		if r.Found {
			b.WriteString(" found=")
			b.WriteString(r.Result)
		}
		if r.ErrCode != "" {
			b.WriteString(" -> ERR ")
			b.WriteString(r.ErrCode)
		}
		if r.LSN > 0 {
			b.WriteString(" [tail=")
			b.WriteString(itoa(r.LSN))
			b.WriteString("]")
		}
		if r.Reason != "" {
			b.WriteString(" // ")
			b.WriteString(r.Reason)
		}
		b.WriteString("\n")
	}
	return b.String()
}
