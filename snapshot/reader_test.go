package snapshot

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
)

// replayUntil 是测试用的判定依据（oracle）：把日志按序号从头重放到 position（含），
// 返回该键当时的值；与 README 中“按序号从头重放核对”是同一套逻辑。
func replayUntil(log []Record, key string, position int64) (string, bool) {
	var value string
	exists := false
	for i := int64(0); i < position && i < int64(len(log)); i++ {
		if log[i].Key == key {
			value, exists = log[i].Value, true
		}
	}
	return value, exists
}

func snapshotLog(t *testing.T, input string, r *Reader, position int64, got ReadResult, basis string) {
	t.Helper()
	t.Logf("input=%s snapshotPoint=%d readPosition=%d result={key:%q value:%q exists:%t baseSeq:%d} basis=%s",
		input, r.SnapshotSeq(), position, got.Key, got.Value, got.Exists, got.BaseSeq, basis)
}

func snapshotErrLog(t *testing.T, input string, r *Reader, position int64, err error, basis string) {
	t.Helper()
	t.Logf("input=%s snapshotPoint=%d readPosition=%d result=<error:%v> basis=%s",
		input, r.SnapshotSeq(), position, err, basis)
}

func mustErr(_ ReadResult, err error) error { return err }
func mustErrWrite(_ int64, err error) error { return err }

// readLatestConsistent 在同一把读锁、同一线性化点上执行双读并用从头重放 oracle 核对，
// 专供并发测试：避免测试自身在锁外观察到中间态。
func readLatestConsistent(t *testing.T, r *Reader, key string) {
	t.Helper()
	r.mu.RLock()
	defer r.mu.RUnlock()
	position := int64(len(r.log))
	got, err := r.readAtLocked(key, position)
	if err != nil {
		t.Errorf("readAtLocked: %v", err)
		return
	}
	wantV, wantE := replayUntil(r.log, key, position)
	if got.Value != wantV || got.Exists != wantE || got.BaseSeq != r.snapSeq {
		t.Errorf("intermediate state: got={%q,%t,base=%d} oracle={%q,%t} snap=%d",
			got.Value, got.Exists, got.BaseSeq, wantV, wantE, r.snapSeq)
	}
}

// 快照与增量交叠：同一键在快照前后均被写，双读必须合并正确。
func TestSnapshotAndDeltaOverlap(t *testing.T) {
	r := New()
	seqA1, _ := r.Write("a", "a1")
	seqB1, _ := r.Write("b", "b1")
	seqA2, _ := r.Write("a", "a2")
	snapAt, err := r.Freeze()
	if err != nil || snapAt != 3 {
		t.Fatalf("freeze: seq=%d err=%v, want 3", snapAt, err)
	}
	t.Logf("input=writes a@%d=a1 b@%d=b1 a@%d=a2 then freeze; snapshotPoint=%d; logRetainedLen=%d basis=日志不清空",
		seqA1, seqB1, seqA2, snapAt, len(r.log))

	seqA3, _ := r.Write("a", "a3")
	seqB2, _ := r.Write("b", "b2")

	cases := []struct {
		position   int64
		key        string
		wantValue  string
		wantExists bool
		basis      string
	}{
		{3, "a", "a2", true, "位点=快照点，纯快照基，增量区间(snapSeq,position]为空"},
		{3, "b", "b1", true, "位点=快照点，纯快照基，无增量"},
		{4, "a", "a3", true, "快照基[a:a2]+应用seq4(a:a3)，最新写胜出"},
		{4, "b", "b1", true, "快照基[b:b1]，seq4是别的键，增量不改b"},
		{5, "a", "a3", true, "seq5写b，不影响a"},
		{5, "b", "b2", true, "快照基[b:b1]+应用seq5(b:b2)"},
		{5, "c", "", false, "快照中不存在c，增量区间也无c"},
	}
	for _, tc := range cases {
		got, err := r.ReadAt(tc.key, tc.position)
		if err != nil {
			t.Fatalf("ReadAt(%q,%d): %v", tc.key, tc.position, err)
		}
		snapshotLog(t, fmt.Sprintf("ReadAt key=%q", tc.key), r, tc.position, got, tc.basis)
		if got.Value != tc.wantValue || got.Exists != tc.wantExists || got.BaseSeq != snapAt {
			t.Fatalf("ReadAt(%q,%d)={%q,%t,base=%d}, want {%q,%t,base=%d}",
				tc.key, tc.position, got.Value, got.Exists, got.BaseSeq,
				tc.wantValue, tc.wantExists, snapAt)
		}
		wantV, wantE := replayUntil(r.log, tc.key, tc.position)
		if wantV != got.Value || wantE != got.Exists {
			t.Fatalf("replay mismatch at (%q,%d): replay={%q,%t} read={%q,%t}",
				tc.key, tc.position, wantV, wantE, got.Value, got.Exists)
		}
	}
	if seqA3 != 4 || seqB2 != 5 {
		t.Fatalf("post-snapshot seqs = %d,%d, want 4,5", seqA3, seqB2)
	}
	if err := r.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// 快照点恰好等于某次写入：seqN 必须被快照覆盖，位点 snapSeq 读到该次写。
func TestFreezeExactlyAtWriteBoundary(t *testing.T) {
	r := New()
	r.Write("k", "v0")
	lastSeq, _ := r.Write("k", "v1")
	snapAt, _ := r.Freeze()
	if snapAt != lastSeq {
		t.Fatalf("snapAt=%d lastWrite=%d", snapAt, lastSeq)
	}
	got, err := r.ReadAt("k", snapAt)
	if err != nil {
		t.Fatal(err)
	}
	snapshotLog(t, "ReadAt key=k at exact boundary", r, snapAt, got,
		"位点=快照点=最后一次写序号，该写已固化进快照，快照覆盖区间[1,snapSeq]")
	if got.Value != "v1" || !got.Exists {
		t.Fatalf("boundary read = %q exists=%t, want v1/true", got.Value, got.Exists)
	}

	got2, err := r.ReadAt("k", snapAt+1)
	if err != nil {
		t.Fatal(err)
	}
	snapshotLog(t, "ReadAt key=k just beyond boundary", r, snapAt+1, got2,
		"位点=snapSeq+1，增量区间(snapSeq,position]为空，回退快照值v1")
	if got2.Value != "v1" {
		t.Fatalf("got %q, want v1", got2.Value)
	}
	if err := r.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 空键、空值、早于快照点的位点必须以可区分原因拒绝。
func TestRejectionsAreDistinguished(t *testing.T) {
	r := New()
	r.Write("x", "1")
	r.Freeze()
	r.Write("x", "2")

	_, err := r.ReadAt("", 2)
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty key read err=%v, want ErrEmptyKey", err)
	}
	snapshotErrLog(t, `ReadAt key="" position=2`, r, 2, err, "空键整体拒绝")

	stalePos := r.SnapshotSeq() - 1
	_, err = r.ReadAt("x", stalePos)
	if !errors.Is(err, ErrReadBeforeSnapshot) {
		t.Fatalf("stale read err=%v, want ErrReadBeforeSnapshot", err)
	}
	snapshotErrLog(t, "ReadAt key=x position=snapSeq-1", r, stalePos, err,
		"位点早于最近快照点，[1,snapSeq)的历史无法据此双读还原，整体拒绝")

	_, err = r.ReadAt("x", -1)
	if !errors.Is(err, ErrInvalidPosition) {
		t.Fatalf("negative position err=%v, want ErrInvalidPosition", err)
	}

	_, err = r.Write("", "v")
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty key write err=%v, want ErrEmptyKey", err)
	}
	_, err = r.Write("k", "")
	if !errors.Is(err, ErrEmptyValue) {
		t.Fatalf("empty value write err=%v, want ErrEmptyValue", err)
	}
}

// 一次失败不得改变日志、快照点与快照内容。
func TestFailureLeavesStateUntouched(t *testing.T) {
	r := New()
	r.Write("k", "v1")
	snapAt, _ := r.Freeze()
	r.Write("k", "v2")

	wantLogLen := len(r.log)
	before, _ := r.ReadAt("k", snapAt)

	errs := []error{
		mustErr(r.ReadAt("", 2)),
		mustErr(r.ReadAt("k", snapAt-1)),
		mustErr(r.ReadAt("k", -1)),
		mustErrWrite(r.Write("", "nope")),
		mustErrWrite(r.Write("k", "")),
	}
	for _, e := range errs {
		if e == nil {
			t.Fatal("all invalid operations must fail")
		}
	}

	if len(r.log) != wantLogLen {
		t.Fatalf("log length changed %d -> %d", wantLogLen, len(r.log))
	}
	if r.SnapshotSeq() != snapAt {
		t.Fatalf("snapshot point changed %d -> %d", snapAt, r.SnapshotSeq())
	}
	after, err := r.ReadAt("k", snapAt)
	if err != nil || after != before {
		t.Fatalf("snapshot content changed: before=%+v after=%+v err=%v", before, after, err)
	}
	t.Logf("input=五次非法读/写; snapshotPoint=%d readPosition=%d result=%+v logLen=%d basis=失败前后日志长度/快照点/快照读逐字段比对",
		r.SnapshotSeq(), snapAt, after, len(r.log))
}

// 反复快照：各位点读都与从头重放 oracle 一致；旧快照点之前的读被拒绝。
func TestRepeatedFreezeAndReplayOracle(t *testing.T) {
	r := New()
	for _, v := range []string{"s1", "s2", "s3"} {
		r.Write("s", v)
	}
	firstSnap, _ := r.Freeze() // 3
	r.Write("s", "s4")
	r.Write("other", "o1")
	secondSnap, _ := r.Freeze() // 5
	r.Write("s", "s5")

	if firstSnap != 3 || secondSnap != 5 {
		t.Fatalf("snaps=%d,%d want 3,5", firstSnap, secondSnap)
	}
	for position := secondSnap; position <= r.CurrentSeq(); position++ {
		got, err := r.ReadAt("s", position)
		if err != nil {
			t.Fatal(err)
		}
		wantV, wantE := replayUntil(r.log, "s", position)
		snapshotLog(t, "ReadAt key=s repeated-freeze", r, position, got,
			fmt.Sprintf("从头重放oracle={%q,%t}，快照基=%d，增量区间=(%d,%d]",
				wantV, wantE, got.BaseSeq, got.BaseSeq, position))
		if got.Value != wantV || got.Exists != wantE {
			t.Fatalf("position %d: got {%q,%t} oracle {%q,%t}",
				position, got.Value, got.Exists, wantV, wantE)
		}
	}
	_, err := r.ReadAt("s", firstSnap)
	if !errors.Is(err, ErrReadBeforeSnapshot) {
		t.Fatalf("read at old snapshot point err=%v, want ErrReadBeforeSnapshot", err)
	}
	if err := r.Verify(); err != nil {
		t.Fatalf("verify after repeated freeze: %v", err)
	}
}

// 反复快照期间并发：写入/快照持续进行时可并发读与自检（配合 -race）；
// 同一实例并发读取同一键、同一位点，结果逐字段相同、无中间态。
func TestConcurrentReadsDuringFreezes(t *testing.T) {
	r := New()
	for i := 0; i < 4; i++ {
		r.Write("hot", fmt.Sprintf("hot%d", i))
	}

	const (
		writers       = 4
		chaosReaders  = 8
		stableReaders = 64
		iterations    = 200
	)
	start := make(chan struct{})
	var writersWG sync.WaitGroup
	var othersWG sync.WaitGroup

	for w := 0; w < writers; w++ {
		writersWG.Add(1)
		go func(id int) {
			defer writersWG.Done()
			<-start
			for j := 0; j < iterations; j++ {
				if _, err := r.Write("hot", fmt.Sprintf("w%d-%d", id, j)); err != nil {
					t.Errorf("write: %v", err)
					return
				}
				if j%3 == 0 {
					if _, err := r.Freeze(); err != nil {
						t.Errorf("freeze: %v", err)
						return
					}
				}
				runtime.Gosched()
			}
		}(w)
	}
	for c := 0; c < chaosReaders; c++ {
		othersWG.Add(1)
		go func() {
			defer othersWG.Done()
			<-start
			for i := 0; i < iterations; i++ {
				// 读“当前最新位点”，并在同一线性化点与从头重放核对，检验写入期间并发读。
				readLatestConsistent(t, r, "hot")
				runtime.Gosched()
			}
		}()
	}
	othersWG.Add(1)
	go func() {
		defer othersWG.Done()
		<-start
		for i := 0; i < iterations; i++ {
			if err := r.Verify(); err != nil {
				t.Errorf("concurrent verify: %v", err)
				return
			}
			runtime.Gosched()
		}
	}()

	close(start)
	writersWG.Wait()

	// 写入停止、最后一次快照冻结后，固定位点，大量并发读必须逐字段相同。
	finalSnap, _ := r.Freeze()
	position := r.CurrentSeq()

	var stable sync.WaitGroup
	results := make(chan ReadResult, stableReaders)
	goStart := make(chan struct{})
	for i := 0; i < stableReaders; i++ {
		stable.Add(1)
		go func() {
			defer stable.Done()
			<-goStart
			got, err := r.ReadAt("hot", position)
			if err != nil {
				t.Errorf("stable read: %v", err)
				return
			}
			results <- got
		}()
	}
	close(goStart)
	stable.Wait()
	close(results)

	var first ReadResult
	count := 0
	for got := range results {
		if count == 0 {
			first = got
		} else if got != first {
			t.Fatalf("stable concurrent reads differ: %+v vs %+v", first, got)
		}
		count++
	}

	othersWG.Wait() // 乱序读者与自检跑完，暴露 -race 下的竞争

	wantV, wantE := replayUntil(r.log, "hot", position)
	t.Logf("input=%d写者持续写并反复快照+%d乱序读者+1自检; snapshotPoint=%d readPosition=%d stableReaders=%d result={%q,%t} oracle={%q,%t} basis=不可变快照副本+固定增量区间(snapSeq,position]逐字段比对",
		writers, chaosReaders, r.SnapshotSeq(), position, count, first.Value, first.Exists, wantV, wantE)
	if finalSnap != r.SnapshotSeq() {
		t.Fatalf("snapshot point moved after writes stopped: %d -> %d", finalSnap, r.SnapshotSeq())
	}
	if first.Value != wantV || first.Exists != wantE {
		t.Fatalf("stable read {%q,%t} != oracle {%q,%t}", first.Value, first.Exists, wantV, wantE)
	}
	if err := r.Verify(); err != nil {
		t.Fatalf("final verify: %v", err)
	}
}
