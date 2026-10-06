package indexstore

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// rngScript 是同时喂给实现与朴素模型的确定性操作脚本。
type rngScript struct {
	rng     *rand.Rand
	pks     []string
	secs    []string
	entries []LogEntry // 模型接受的写入，用于在实现侧构造崩溃现场
}

// TestRandomDifferential 对大量随机写入序列，枚举“索引已吸收前缀长度”
// 的每个可能值与水位的每个可能位置，重放后比对：
//   - 每种错误/成功输出与朴素模型一致；
//   - 追赶后的索引 == 从主表完整重建；
//   - 相同种子、相同崩溃点重放结果完全相同。
func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 400; seed++ {
		script := buildScript(seed)
		// 枚举每个前缀长度与水位位置（规模小，全部组合可承受）。
		for applied := 0; applied <= len(script.entries); applied++ {
			for wm := 0; wm <= applied; wm++ {
				if !runDifferential(t, seed, script, applied, wm) {
					t.Fatalf("seed=%d applied=%d wm=%d mismatch", seed, applied, wm)
				}
			}
		}
	}
}

func buildScript(seed int64) *rngScript {
	r := rand.New(rand.NewSource(seed))
	sc := &rngScript{
		rng:  r,
		pks:  []string{"p1", "p2", "p3", "p4"},
		secs: []string{"a", "b", "c"},
	}
	m := newNaiveModel()
	n := 12 + r.Intn(14)
	for i := 0; i < n; i++ {
		pk := sc.pks[r.Intn(len(sc.pks))]
		switch r.Intn(5) {
		case 0, 1, 2: // 写非空键
			sec := sc.secs[r.Intn(len(sc.secs))]
			out := m.put(pk, sec, true)
			sc.recordIfAccepted(m, out.lsn)
		case 3: // 置空
			out := m.put(pk, "", false)
			sc.recordIfAccepted(m, out.lsn)
		case 4: // 删除
			out := m.del(pk)
			sc.recordIfAccepted(m, out.lsn)
		}
	}
	// 直接从模型复制最终接受序列，得到实现侧需要的 LogEntry 形式。
	sc.entries = sc.entries[:0]
	for _, e := range m.log {
		sc.entries = append(sc.entries, LogEntry{
			LSN: e.lsn, Op: e.op, PK: e.pk,
			OldSec: e.oldSec, HasOld: e.hasOld,
			NewSec: e.newSec, HasNew: e.hasNew,
		})
	}
	return sc
}

func (sc *rngScript) recordIfAccepted(m *naiveModel, lsn int) {
	_ = m
	_ = lsn // entries 最终由模型日志统一重建
}

// runDifferential 用同一脚本、同一崩溃现场驱动两边：
//  1. 实现从 (applied,wm) 现场打开；模型置为等价状态；
//  2. 随机交错执行“后续写入/删除”与“分批追赶”；每步两边都做，
//     比对错误类别与成功结果；
//  3. 最终两边都追到末尾，比对索引、自检与水位。
func runDifferential(t *testing.T, seed int64, sc *rngScript, applied0, wm0 int) bool {
	// 本轮交错可能追加写入；复制脚本以免污染同一 seed 的其它崩溃点组合。
	entries := make([]LogEntry, len(sc.entries))
	copy(entries, sc.entries)
	d := fabricatePrefixCrash(t, entries, applied0, wm0)
	s, err := Open(d, Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := newNaiveModel()
	// 用模型重放全部已存在日志，构造等价主表；再施加崩溃状态。
	m.log = nil
	m.rows = map[string]modelRow{}
	m.applied = applied0
	m.wmLag = applied0 - wm0
	m.caught = wm0 == len(entries)
	for _, e := range sc.entries {
		me := modelEntry{
			lsn: e.LSN, op: e.Op, pk: e.PK,
			oldSec: e.OldSec, hasOld: e.HasOld,
			newSec: e.NewSec, hasNew: e.HasNew,
		}
		m.log = append(m.log, me)
		if e.Op == LogDelete {
			r := m.rows[e.PK]
			r.alive = false
			m.rows[e.PK] = r
		} else {
			m.rows[e.PK] = modelRow{sec: e.NewSec, hasSec: e.HasNew, alive: true}
		}
	}

	r := rand.New(rand.NewSource(seed*7919 + 1))
	failMsg := ""
	check := func(step string, got error, want modelOutcome) bool {
		gotCode := ErrorCode(-1)
		if got != nil {
			gotCode = got.(*Error).Code
		}
		if gotCode != want.err {
			failMsg = fmt.Sprintf("step=%s got=%v want=%v", step, got, codeName(want.err))
			return false
		}
		return true
	}

	// 交错若干轮：可能在未追完时写入，制造“追赶中新写入”。
	for round := 0; round < 10; round++ {
		switch r.Intn(3) {
		case 0: // 写入或删除
			pk := sc.pks[r.Intn(len(sc.pks))]
			if r.Intn(2) == 0 {
				sec := sc.secs[r.Intn(len(sc.secs))]
				_, gerr := s.Put(pk, sec, true)
				w := m.put(pk, sec, true)
				if w.err == -1 {
					entries = append(entries, LogEntry{
						LSN: len(sc.entries) + 1, Op: LogUpsert, PK: pk,
						NewSec: sec, HasNew: true,
						OldSec: m.log[len(m.log)-1].oldSec,
						HasOld: m.log[len(m.log)-1].hasOld,
					})
				}
				if !check(fmt.Sprintf("put %s=%s", pk, sec), gerr, w) {
					return failAt(t, seed, applied0, wm0, failMsg, s, m)
				}
			} else {
				_, gerr := s.Delete(pk)
				w := m.del(pk)
				if w.err == -1 {
					le := m.log[len(m.log)-1]
					entries = append(entries, LogEntry{
						LSN: len(sc.entries) + 1, Op: LogDelete, PK: pk,
						OldSec: le.oldSec, HasOld: le.hasOld,
					})
				}
				if !check("delete "+pk, gerr, w) {
					return failAt(t, seed, applied0, wm0, failMsg, s, m)
				}
			}
		case 1: // 小批量追赶
			batch := 1 + r.Intn(3)
			// 缺口场景不在差分测试中；两边都按正常日志追。
			_, gdone, gerr := s.CatchUp(batch)
			w := m.catchUp(batch)
			if !check("catchup", gerr, w) {
				return failAt(t, seed, applied0, wm0, failMsg, s, m)
			}
			if gerr == nil && gdone != w.done {
				failMsg = fmt.Sprintf("catchup done: got=%v want=%v", gdone, w.done)
				return failAt(t, seed, applied0, wm0, failMsg, s, m)
			}
		case 2: // stale 期间的查询语义对照
			sec := sc.secs[r.Intn(len(sc.secs))]
			_, _, gerr := s.Lookup(sec)
			w := m.lookup(sec)
			if !check("lookup "+sec, gerr, w) {
				return failAt(t, seed, applied0, wm0, failMsg, s, m)
			}
		}
	}

	// 全部追完（两边），逐键比对。
	catchUpFully(t, s, 2)
	for m.applied < len(m.log) {
		m.catchUp(100)
	}
	want := m.rebuildIndex()
	got := indexMap(s)
	if !mapsEqual(got, want) {
		failMsg = fmt.Sprintf("final index got=%v want=%v", got, want)
		return failAt(t, seed, applied0, wm0, failMsg, s, m)
	}
	diffs, verr := s.Verify()
	if verr != nil || len(diffs) != 0 {
		failMsg = fmt.Sprintf("verify: err=%v diffs=%v", verr, diffs)
		return failAt(t, seed, applied0, wm0, failMsg, s, m)
	}
	if s.Watermark() != len(entries) {
		failMsg = fmt.Sprintf("watermark=%d tail=%d", s.Watermark(), len(entries))
		return failAt(t, seed, applied0, wm0, failMsg, s, m)
	}
	return true
}

func failAt(t *testing.T, seed int64, applied, wm int, msg string,
	s *Store, m *naiveModel) bool {
	t.Helper()
	t.Logf("DIFFERENTIAL FAIL seed=%d applied=%d wm=%d\n%s\n--- implementation op log ---\n%s",
		seed, applied, wm, msg, formatOpLog(s.OpLog()))
	// 渲染模型期望主表，便于人工复现。
	var pks []string
	for pk, r := range m.rows {
		if r.alive {
			pks = append(pks, pk)
		}
	}
	sort.Strings(pks)
	var b strings.Builder
	for _, pk := range pks {
		r := m.rows[pk]
		fmt.Fprintf(&b, "  %s -> sec=%q has=%v\n", pk, r.sec, r.hasSec)
	}
	t.Logf("--- model table ---\n%s", b.String())
	return false
}

// TestDeterministicReplay：相同脚本+相同崩溃点两次重放，磁盘字节与
// 最终索引完全一致。
func TestDeterministicReplay(t *testing.T) {
	sc := buildScript(77)
	run := func() (map[string]string, map[string]string) {
		d := fabricatePrefixCrash(t, sc.entries, 3, 1)
		s, _ := Open(d, Options{})
		catchUpFully(t, s, 2)
		files := map[string]string{}
		for k, v := range d.files {
			files[k] = v
		}
		return indexMap(s), files
	}
	idx1, files1 := run()
	idx2, files2 := run()
	if !mapsEqual(idx1, idx2) || !mapsEqual(files1, files2) {
		t.Fatal("replay not byte-for-byte deterministic")
	}
}

// TestConcurrentWritersAndCatcher：并发写入与追赶，所有可见结果必须要么
// stale、要么与完整重建一致（串行化由单一互斥锁保证）。
func TestConcurrentWritersAndCatcher(t *testing.T) {
	d := NewDisk()
	s, _ := Open(d, Options{})
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() { // catcher
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, done, err := s.CatchUp(1)
			if err != nil && err.(*Error).Code != ErrInvalidArgument {
				t.Errorf("catchup: %v", err)
				return
			}
			if done {
				return
			}
		}
	}()

	writer := func(pk string, secs []string) {
		defer wg.Done()
		for _, sec := range secs {
			if sec == "" {
				_, _ = s.Put(pk, "", false)
			} else if _, err := s.Put(pk, sec, true); err != nil {
				// 唯一冲突是合法并发结果。
				if ge, ok := err.(*Error); !ok || ge.Code != ErrUniqueConflict {
					t.Errorf("put: %v", err)
				}
			}
		}
	}
	wg.Add(3)
	go writer("p1", []string{"a", "b", "", "a", "c"})
	go writer("p2", []string{"b", "", "c", "a"})
	go writer("p3", []string{"c", "a", ""})
	wg.Wait()
	close(stop)

	// 写入 goroutine 结束后把剩余日志追完。
	catchUpFully(t, s, 1)
	if s.Stale() {
		t.Fatal("must be caught up at end")
	}
	diffs, err := s.Verify()
	if err != nil || len(diffs) != 0 {
		t.Fatalf("verify: %v %v", diffs, err)
	}
	// 主表读取与最终索引一致。
	want := rebuildExpectation(scEntriesOf(s))
	if !mapsEqual(indexMap(s), want) {
		t.Fatalf("concurrent result diverges: %v want %v", indexMap(s), want)
	}
}

func scEntriesOf(s *Store) []LogEntry {
	out := make([]LogEntry, len(s.entries))
	copy(out, s.entries)
	return out
}
