package indexstore

import (
	"strconv"
	"strings"
	"testing"
)

// 追赶在每个批边界（以及每条日志的提交步骤）被打断：枚举每个崩溃点，
// 重启继续追赶，最终索引必须与完整重建一致，水位等于日志末尾。
func TestCrashAtEveryApplyAndBatchBoundary(t *testing.T) {
	entries := scenarioEntries()
	// 每次追赶用 batch=2；枚举崩溃步骤：
	//   index-apply@lsn k（第 k 条的效果+applied 落盘前）
	//   watermark（某次触底批次的水位落盘前）
	// 采用“计数型”钩子：第 crashTick 次匹配步骤崩溃。
	steps := []string{"index-apply", "watermark"}
	for _, step := range steps {
		for tick := 1; tick <= len(entries)+2; tick++ {
			d := fabricatePrefixCrash(t, entries, 0, 0)
			crashed := loopCatchUpWithCrash(d, step, tick, 2)
			s, err := Open(d, Options{})
			if err != nil {
				t.Fatalf("step=%s tick=%d reopen: %v", step, tick, err)
			}
			// 若计数从未命中（tick 超出实际步骤数），crashed=false，
			// 此时上面的循环应已追完。
			if s.Stale() {
				catchUpFully(t, s, 2)
			}
			want := rebuildExpectation(entries)
			if got := indexMap(s); !mapsEqual(got, want) {
				t.Fatalf("step=%s tick=%d crashed=%v index=%v want=%v",
					step, tick, crashed, got, want)
			}
			if s.Watermark() != len(entries) {
				t.Fatalf("step=%s tick=%d wm=%d want=%d",
					step, tick, s.Watermark(), len(entries))
			}
			if diffs, _ := s.Verify(); len(diffs) != 0 {
				t.Fatalf("step=%s tick=%d diffs=%v", step, tick, diffs)
			}
		}
	}
}

// loopCatchUpWithCrash 在反复“打开->追赶直到崩溃/追完”的循环里运行，
// 直到索引追完或注入崩溃发生（崩溃后返回 true）。
func loopCatchUpWithCrash(d *Disk, step string, tick, batch int) bool {
	seen := 0
	for {
		var crashed bool
		done := Run(d, func(s *Store) {
			d.SetCrashHook(func(gotStep string) bool {
				if gotStep != step {
					return false
				}
				seen++
				if seen == tick {
					return true
				}
				return false
			})
			for s.Stale() {
				_, d, err := s.CatchUp(batch)
				if err != nil {
					panic(err)
				}
				if d {
					return
				}
			}
		})
		crashed = done
		s, err := Open(d, Options{})
		if err != nil {
			panic(err)
		}
		if !s.Stale() {
			return crashed
		}
		if crashed {
			return true
		}
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// 日志缺口位于不同位置：重写磁盘 WAL 制造缺口（把某条的 LSN 改大），
// 追赶必须报 ErrLogGap，且索引与水位保持不变。
func TestLogGapAtEveryPosition(t *testing.T) {
	entries := scenarioEntries()
	for gapAt := 2; gapAt <= len(entries); gapAt++ {
		d := fabricateGapDisk(t, entries, gapAt)
		// 让索引已吸收 gapAt-1 条（缺口前一位），水位停在更早处。
		s, err := Open(d, Options{})
		if err != nil {
			// Open 对从 1 开始的整体连续性校验可能直接拒绝；这同样合法，
			// 但我们构造的缺口起点在 gapAt，load 阶段也会发现：
			if ge, ok := err.(*Error); ok && ge.Code == ErrLogGap {
				continue
			}
			t.Fatalf("gapAt=%d unexpected open err: %v", gapAt, err)
		}
		wmBefore := s.Watermark()
		appliedBefore := s.Applied()
		idxBefore := indexMap(s)
		_, _, err = s.CatchUp(100)
		expectCode(t, err, ErrLogGap)
		if s.Watermark() != wmBefore || s.Applied() != appliedBefore {
			t.Fatalf("gapAt=%d progress changed: wm %d->%d applied %d->%d",
				gapAt, wmBefore, s.Watermark(), appliedBefore, s.Applied())
		}
		if !mapsEqual(indexMap(s), idxBefore) {
			t.Fatalf("gapAt=%d index mutated", gapAt)
		}
	}
}

// fabricateGapDisk 复制前缀崩溃现场后，把第 gapAt 条起的所有 WAL 记录
// LSN 加 1（中间留出一个洞），主表内容保持“与（有洞的）日志一致”——
// 真实场景中 LSN 由 WAL 分配，主表不会反映缺口，因此只改 WAL 文本。
func fabricateGapDisk(t *testing.T, entries []LogEntry, gapAt int) *Disk {
	t.Helper()
	d := fabricatePrefixCrash(t, entries, gapAt-1, gapAt-1)
	// 重写 WAL：前 gapAt-1 条不动，之后每条 LSN+1。
	var text string
	for i, e := range entries {
		if i+1 >= gapAt {
			e.LSN++
		}
		text += encodeEntry(e) + "\n"
	}
	d.files["wal"] = text
	return d
}

// 追赶期间：二级键查询报 stale；按主键读取始终有效；写入仍按最新主表
// 被接受或拒绝；追完后新写入不会被遗漏或重复应用。
func TestBehaviorWhileCatchingUp(t *testing.T) {
	entries := scenarioEntries()
	// 索引/水位停在中途。
	half := len(entries) / 2
	d := fabricatePrefixCrash(t, entries, half, half)
	s, _ := Open(d, Options{})

	// 二级键查询与自检：stale。
	if !s.Stale() {
		t.Fatal("want stale mid-catch-up")
	}
	if _, _, err := s.Lookup("a"); err == nil {
		t.Fatal("lookup must be stale")
	}
	if _, err := s.Verify(); err == nil {
		t.Fatal("verify must be stale")
	}

	// 按主键读取不受影响：直接读主表。
	if _, ok, err := s.Get("p1"); err != nil || !ok {
		t.Fatalf("get by pk must work while stale: ok=%v err=%v", ok, err)
	}

	// 追赶期间的唯一冲突判定必须基于主表最新状态。先看主表里谁持有 b。
	rows := map[string]Row{}
	for _, e := range entries {
		if e.Op == LogDelete {
			delete(rows, e.PK)
		} else {
			rows[e.PK] = Row{PK: e.PK, Sec: e.NewSec, HasSec: e.HasNew}
		}
	}
	holderB := rows["p2"]
	if !holderB.HasSec || holderB.Sec != "b" {
		t.Fatalf("test premise broken: p2=%+v", holderB)
	}
	if _, err := s.Put("intruder", "b", true); err == nil {
		t.Fatal("conflict must be detected even while index stale")
	}

	// 被接受的新写入（释放一个键、引入新键），追赶不得遗漏/重复。
	tailBefore := s.Tail()
	if _, err := s.Put("p2", "z9", true); err != nil {
		t.Fatal(err)
	}
	if s.Tail() != tailBefore+1 {
		t.Fatal("accepted write must consume one lsn")
	}

	catchUpFully(t, s, 4)
	if s.Watermark() != s.Tail() {
		t.Fatalf("wm=%d tail=%d", s.Watermark(), s.Tail())
	}
	// 期望索引 = 在原场景主表上追加 p2:z9。
	rows["p2"] = Row{PK: "p2", Sec: "z9", HasSec: true}
	want := map[string]string{}
	for _, r := range rows {
		if r.HasSec && r.Sec != "" {
			want[r.Sec] = r.PK
		}
	}
	if got := indexMap(s); !mapsEqual(got, want) {
		t.Fatalf("index=%v want=%v", got, want)
	}
	if diffs, _ := s.Verify(); len(diffs) != 0 {
		t.Fatalf("diffs=%v", diffs)
	}
}

// 自检三类不一致可区分（直接在磁盘上构造损坏的索引）。
func TestVerifyDiffs(t *testing.T) {
	d := NewDisk()
	s, _ := Open(d, Options{})
	mustPut(s, "p1", "a", true)
	mustPut(s, "p2", "b", true)
	mustPut(s, "p3", "c", true)
	catchUpFully(t, s, 10)

	// 损坏：多余条目 ghost:x；缺失 a（删条目）；b 错指 p9。
	d.files["index:x"] = "ghost"
	delete(d.files, "index:a")
	d.files["index:b"] = "p9"
	s2, err := Open(d, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// 损坏不改变进度，索引不算 stale。
	diffs, err := s2.Verify()
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[DiffKind]Diff{}
	for _, df := range diffs {
		byKind[df.Kind] = df
	}
	ex, ok1 := byKind[DiffExtra]
	ms, ok2 := byKind[DiffMissing]
	wo, ok3 := byKind[DiffWrongOwner]
	if !ok1 || ex.Sec != "x" || ex.GotOwner != "ghost" {
		t.Fatalf("extra diff wrong: %+v", byKind)
	}
	if !ok2 || ms.Sec != "a" || ms.WantOwner != "p1" {
		t.Fatalf("missing diff wrong: %+v", byKind)
	}
	if !ok3 || wo.Sec != "b" || wo.GotOwner != "p9" || wo.WantOwner != "p2" {
		t.Fatalf("wrong-owner diff wrong: %+v", byKind)
	}
}

// 错误优先级：参数非法先于其他类别；lookup 参数非法先于 stale。
func TestErrorPrecedence(t *testing.T) {
	d := NewDisk()
	s, _ := Open(d, Options{})
	mustPut(s, "p1", "a", true) // 索引现在 stale
	_, _, err := s.Lookup("")
	expectCode(t, err, ErrInvalidArgument)
	_, _, err = s.CatchUp(0)
	expectCode(t, err, ErrInvalidArgument)
	_, err = s.Put("", "a", true)
	expectCode(t, err, ErrInvalidArgument)
	_, err = s.Delete("")
	expectCode(t, err, ErrInvalidArgument)
	// stale 优先于“查不到”：lookup 不存在的键时仍报 stale。
	_, _, err = s.Lookup("nope")
	expectCode(t, err, ErrIndexStale)
}

// 操作日志包含输入、输出与判定依据，并可渲染复现。
func TestOpLogContents(t *testing.T) {
	d := NewDisk()
	s, _ := Open(d, Options{})
	mustPut(s, "p1", "a", true)
	_, err := s.Put("p2", "a", true)
	expectCode(t, err, ErrUniqueConflict)
	out := formatOpLog(s.OpLog())
	if !strings.Contains(out, "unique-conflict") ||
		!strings.Contains(out, "owned by pk=p1") {
		t.Fatalf("op log missing rationale:\n%s", out)
	}
	if strconv.Itoa(s.Tail()) == "" {
		t.Fatal("tail empty")
	}
}
