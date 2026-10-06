package indexstore

import (
	"sort"
	"strconv"
	"strings"
)

// index 是持久化的唯一二级索引，另带两个持久化进度：
//
//   - applied：索引内容实际已吸收到的 LSN，随索引条目逐项同步骤落盘，
//     因此“索引含某 LSN 的效果”与“applied >= 该 LSN”在崩溃点上等价；
//   - watermark：题目规定的水位，含义是 LSN<=watermark 的效果必然已在
//     索引中。水位仅在追赶批次边界推进，允许落后于 applied，绝不超前。
//
// 追赶重放是幂等的状态归并：每条日志只检查/修正其涉及的有限键，
// 单条开销 O(1)，与主表行数、索引条目数无关。
type index struct {
	disk      *Disk
	entries   map[string]string // 非空二级键 -> 主键
	putLSN    map[string]int    // 每个键当前条目来源的 Put 日志 LSN（持久化）
	removedBy map[string]int    // 每个键最近一次“释放/删除”的 LSN（墓碑，持久化）
	applied   int
	watermark int
}

func newIndex(d *Disk) *index {
	return &index{disk: d, entries: map[string]string{},
		putLSN: map[string]int{}, removedBy: map[string]int{}}
}

// load 从磁盘恢复索引条目与 applied/watermark（骨架）。
func (ix *index) load() error {
	for k, v := range ix.disk.files {
		if strings.HasPrefix(k, "index:") {
			ix.entries[k[len("index:"):]] = v
		}
		if strings.HasPrefix(k, "indexlsn:") {
			n, err := strconv.Atoi(v)
			if err != nil {
				return err
			}
			ix.putLSN[k[len("indexlsn:"):]] = n
		}
		if strings.HasPrefix(k, "indexrm:") {
			n, err := strconv.Atoi(v)
			if err != nil {
				return err
			}
			ix.removedBy[k[len("indexrm:"):]] = n
		}
	}
	if v, ok := ix.disk.get("applied"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return err
		}
		ix.applied = n
	}
	if v, ok := ix.disk.get("watermark"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return err
		}
		ix.watermark = n
	}
	return nil
}

// applyOne 幂等地吸收一条日志；lsn<=applied 时跳过（骨架）。
func (ix *index) applyOne(e LogEntry) {
	if e.LSN <= ix.applied {
		return
	}
	// 状态归并规则（对任意前缀状态幂等）：
	//   put(sec,pk)@L：若 putLSN[sec] 与 removedBy[sec] 都 < L，则 sec
	//     的最新效果是“指向 pk”，写入；否则保持现状。
	//   release(sec)@L：若 removedBy[sec] < L，则记录墓碑 removedBy=L；
	//     当条目当前在场且其 putLSN < L 时移除条目。条目 putLSN >= L
	//     （已被更晚的 put 覆盖）时只更新墓碑，不动条目。
	changes := map[string]string{}
	var deletes []string
	if e.Op == LogUpsert && e.HasNew && e.NewSec != "" {
		olderPut := true
		if l, ok := ix.putLSN[e.NewSec]; ok && l >= e.LSN {
			olderPut = false
		}
		olderRM := true
		if l, ok := ix.removedBy[e.NewSec]; ok && l >= e.LSN {
			olderRM = false
		}
		if olderPut && olderRM {
			changes["index:"+e.NewSec] = e.PK
			changes["indexlsn:"+e.NewSec] = strconv.Itoa(e.LSN)
			// 同键沿用（新旧相同）不产生释放；仅当旧键是另一个键时，
			// 下面统一处理对旧键的释放。
		}
	}
	// 旧键被本次变更释放（改键、置空、删除）。
	oldSec := e.OldSec
	oldIsNew := e.Op == LogUpsert && e.HasNew && e.NewSec == oldSec
	if e.HasOld && oldSec != "" && !oldIsNew {
		olderRM := true
		if l, ok := ix.removedBy[oldSec]; ok && l >= e.LSN {
			olderRM = false
		}
		if olderRM {
			changes["indexrm:"+oldSec] = strconv.Itoa(e.LSN)
			if cur, ok := ix.entries[oldSec]; ok && cur == e.PK &&
				ix.putLSN[oldSec] < e.LSN {
				deletes = append(deletes, "index:"+oldSec)
				deletes = append(deletes, "indexlsn:"+oldSec)
			}
		}
	}
	// 即使本条日志不改变任何键（空键写入、删除空键行、或其效果已由
	// 更晚的前缀条目体现），applied 也必须推进：“吸收”包含确认无变化。
	ix.commitEntry(e.LSN, changes, deletes)
}

// rebuildFrom 用主表当前内容完整重建索引。nowLSN 为主表对应的日志末尾
// LSN，作为每个存活条目的来源 LSN 写入（完整重建语义下它们都是“当前”值）。
func (ix *index) rebuildFrom(rows []Row, nowLSN int) {
	want := map[string]string{}
	for _, r := range rows {
		if r.HasSec && r.Sec != "" {
			want[r.Sec] = r.PK
		}
	}
	changes := map[string]string{}
	var deletes []string
	for sec, pk := range want {
		if cur, ok := ix.entries[sec]; !ok || cur != pk {
			changes["index:"+sec] = pk
			changes["indexlsn:"+sec] = strconv.Itoa(nowLSN)
		}
	}
	allSecs := map[string]struct{}{}
	for sec := range ix.entries {
		allSecs[sec] = struct{}{}
	}
	for sec := range ix.putLSN {
		allSecs[sec] = struct{}{}
	}
	for sec := range allSecs {
		if _, keep := want[sec]; !keep {
			deletes = append(deletes, "index:"+sec)
			deletes = append(deletes, "indexlsn:"+sec)
			deletes = append(deletes, "indexrm:"+sec)
		}
	}
	// 重建不改变水位语义（测试/诊断用途，与追赶结果做逐项对照）。
	ix.disk.stagedCommit("rebuild", changes, deletes)
	for k, v := range changes {
		if rest, ok := cutIndexKey(k); ok {
			ix.entries[rest] = v
		}
		if rest, ok := strings.CutPrefix(k, "indexlsn:"); ok {
			n, _ := strconv.Atoi(v)
			ix.putLSN[rest] = n
		}
	}
	for _, k := range deletes {
		if rest, ok := strings.CutPrefix(k, "indexlsn:"); ok {
			delete(ix.putLSN, rest)
			continue
		}
		if rest, ok := strings.CutPrefix(k, "indexrm:"); ok {
			delete(ix.removedBy, rest)
			continue
		}
		if rest, ok := cutIndexKey(k); ok {
			delete(ix.entries, rest)
		}
	}
	for sec := range want {
		if _, ok := changes["index:"+sec]; ok {
			ix.putLSN[sec] = nowLSN
		}
	}
}

// cutIndexKey 只识别数据键 "index:<sec>"。CutPrefix 要求冒号精确出现在
// index 之后，因此 indexlsn:/indexrm: 元数据键不会被误判；这里再显式
// 判一次长度，杜绝空键。
func cutIndexKey(k string) (string, bool) {
	const p = "index:"
	if len(k) > len(p) && k[:len(p)] == p {
		return k[len(p):], true
	}
	return "", false
}

// compareTo 返回索引与主表的逐项差异（多余/缺失/错指，三类可区分，骨架）。
func (ix *index) compareTo(rows []Row) []Diff {
	want := map[string]string{}
	for _, r := range rows {
		if r.HasSec && r.Sec != "" {
			want[r.Sec] = r.PK
		}
	}
	var diffs []Diff
	secs := map[string]struct{}{}
	for s := range want {
		secs[s] = struct{}{}
	}
	for s := range ix.entries {
		secs[s] = struct{}{}
	}
	for s := range secs {
		got, gotOK := ix.entries[s]
		w, wantOK := want[s]
		switch {
		case gotOK && wantOK && got != w:
			diffs = append(diffs, Diff{Kind: DiffWrongOwner, Sec: s, GotOwner: got, WantOwner: w})
		case gotOK && !wantOK:
			diffs = append(diffs, Diff{Kind: DiffExtra, Sec: s, GotOwner: got})
		case !gotOK && wantOK:
			diffs = append(diffs, Diff{Kind: DiffMissing, Sec: s, WantOwner: w})
		}
	}
	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].Sec != diffs[j].Sec {
			return diffs[i].Sec < diffs[j].Sec
		}
		return diffs[i].Kind < diffs[j].Kind
	})
	return diffs
}

// commitEntry 把一个键的增删与 applied=lsn 放进同一个崩溃原子步骤：
// 崩溃后要么键效果与 applied 都在，要么都不在，二者不会各说各话。
// 若该步骤前发生注入崩溃，内存与磁盘都不变（applied 也不前进）。
func (ix *index) commitEntry(lsn int, changes map[string]string, deletes []string) {
	if changes == nil {
		changes = map[string]string{}
	}
	changes["applied"] = strconv.Itoa(lsn)
	if !ix.disk.stagedCommit("index-apply", changes, deletes) {
		return
	}
	for k, v := range changes {
		if k == "applied" {
			continue
		}
		if rest, ok := strings.CutPrefix(k, "indexlsn:"); ok {
			n, _ := strconv.Atoi(v)
			ix.putLSN[rest] = n
			continue
		}
		if rest, ok := strings.CutPrefix(k, "indexrm:"); ok {
			n, _ := strconv.Atoi(v)
			ix.removedBy[rest] = n
			continue
		}
		if rest, ok := strings.CutPrefix(k, "index:"); ok {
			ix.entries[rest] = v
		}
	}
	for _, k := range deletes {
		if rest, ok := strings.CutPrefix(k, "indexlsn:"); ok {
			delete(ix.putLSN, rest)
			continue
		}
		if rest, ok := strings.CutPrefix(k, "indexrm:"); ok {
			delete(ix.removedBy, rest)
			continue
		}
		if rest, ok := strings.CutPrefix(k, "index:"); ok {
			delete(ix.entries, rest)
		}
	}
	ix.applied = lsn
}

// commitWatermark 在批次边界持久化水位；watermark<=applied 恒成立。
func (ix *index) commitWatermark() bool {
	if !ix.disk.stagedCommit("watermark",
		map[string]string{"watermark": strconv.Itoa(ix.applied)}, nil) {
		return false
	}
	ix.watermark = ix.applied
	return true
}
