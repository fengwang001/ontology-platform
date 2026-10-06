package indexstore

import (
	"strconv"
)

// failer 由 *testing.T 与 *testing.B 共同满足。
type failer interface {
	Helper()
	Fatalf(format string, args ...any)
}

// fabricatePrefixCrash 在磁盘上构造一次精确的崩溃现场：
//   - WAL 与主表包含 entries 的全部效果（同步提交，永不落后）；
//   - 索引恰好吸收前 appliedLSN 条日志的效果（applied=appliedLSN）；
//   - 水位停在 watermarkLSN（0<=watermarkLSN<=appliedLSN<=len(entries)）。
//
// 这直接覆盖“索引可能已含任意长度的、序号大于水位的前缀，水位绝不超前”。
func fabricatePrefixCrash(t failer, entries []LogEntry, appliedLSN, watermarkLSN int) *Disk {
	t.Helper()
	if watermarkLSN > appliedLSN || appliedLSN > len(entries) || watermarkLSN < 0 {
		t.Fatalf("bad crash params wm=%d applied=%d tail=%d",
			watermarkLSN, appliedLSN, len(entries))
	}
	d := NewDisk()
	tbl := newTable(d)
	var walText string
	for _, e := range entries {
		// 绕过崩溃钩子，逐步骤同步落盘 WAL+主表。
		changes := map[string]string{}
		var deletes []string
		key := "table:" + e.PK
		if e.Op == LogDelete {
			deletes = append(deletes, key)
		} else {
			changes[key] = encodeRow(e.NewSec, e.HasNew)
		}
		walText += encodeEntry(e) + "\n"
		changes["wal"] = walText
		d.stagedCommit("seed", changes, deletes)
		tbl.memoryApply(e)
	}
	// 用前缀日志朴素构造索引内容。
	prefix := map[string]string{}
	owner := map[string]string{}
	for _, e := range entries[:appliedLSN] {
		if e.HasOld && e.OldSec != "" {
			if cur, ok := owner[e.OldSec]; ok && cur == e.PK {
				delete(owner, e.OldSec)
				delete(prefix, e.OldSec)
			}
		}
		if e.Op == LogUpsert && e.HasNew && e.NewSec != "" {
			if cur, ok := owner[e.NewSec]; !ok || cur == e.PK {
				owner[e.NewSec] = e.PK
				prefix[e.NewSec] = e.PK
			}
		}
	}
	changes := map[string]string{
		"applied":   strconv.Itoa(appliedLSN),
		"watermark": strconv.Itoa(watermarkLSN),
	}
	for sec, pk := range prefix {
		changes["index:"+sec] = pk
	}
	var deletes []string
	d.stagedCommit("seed-index", changes, deletes)
	// 覆盖一次：写入每个前缀条目的来源 LSN（朴素扫描记录最后一次 put）。
	src := map[string]int{}
	rm := map[string]int{}
	for _, e := range entries[:appliedLSN] {
		oldIsNew := e.Op == LogUpsert && e.HasNew && e.NewSec == e.OldSec
		if e.HasOld && e.OldSec != "" && !oldIsNew {
			rm[e.OldSec] = e.LSN
		}
		if e.Op == LogUpsert && e.HasNew && e.NewSec != "" {
			src[e.NewSec] = e.LSN
		}
	}
	meta := map[string]string{}
	for sec, l := range src {
		meta["indexlsn:"+sec] = strconv.Itoa(l)
	}
	for sec, l := range rm {
		meta["indexrm:"+sec] = strconv.Itoa(l)
	}
	d.stagedCommit("seed-index-lsn", meta, nil)
	return d
}

// catchUpFully 用给定批大小把索引追赶到日志末尾，返回各批 (reached,done)。
func catchUpFully(t failer, s *Store, batch int) []struct {
	reached int
	done    bool
} {
	t.Helper()
	var steps []struct {
		reached int
		done    bool
	}
	for {
		reached, done, err := s.CatchUp(batch)
		if err != nil {
			t.Fatalf("unexpected catch-up error: %v", err)
		}
		steps = append(steps, struct {
			reached int
			done    bool
		}{reached, done})
		if done {
			return steps
		}
	}
}

func indexMap(s *Store) map[string]string {
	out := map[string]string{}
	for k, v := range s.index.entries {
		out[k] = v // ix.entries 只含数据键，元数据在 putLSN/removedBy
	}
	return out
}
