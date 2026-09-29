package snapread

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// logCase 打印单测要求的判定要素：输入、快照点、读位点、返回值与判定依据。
func logCase(t *testing.T, tag, key string, seq, snapSeq int, res *Result, err error) {
	t.Helper()
	switch {
	case err != nil:
		t.Logf("[%s] input={key:%q, seq:%d}, snapshotPoint=%d, readSeq=%d, rejected err=%v, basis=拒绝，未合并任何记录",
			tag, key, seq, snapSeq, seq, err)
	case !res.Exists:
		t.Logf("[%s] input={key:%q, seq:%d}, snapshotPoint=%d, readSeq=%d, return=<不存在>, basis=%s",
			tag, key, seq, snapSeq, res.Seq, res.Basis)
	default:
		t.Logf("[%s] input={key:%q, seq:%d}, snapshotPoint=%d, readSeq=%d, return=%q, basis=%s",
			tag, key, seq, snapSeq, res.Seq, string(res.Value), res.Basis)
	}
}

func readAndLog(t *testing.T, tag string, s *Store, seq int, key string) (*Result, error) {
	t.Helper()
	res, err := s.ReadAt(seq, key)
	logCase(t, tag, key, seq, s.SnapshotSeq(), res, err)
	return res, err
}

func assertValue(t *testing.T, res *Result, want string) {
	t.Helper()
	if !res.Exists {
		t.Fatalf("期望键存在且值为 %q，实际不存在", want)
	}
	if string(res.Value) != want {
		t.Fatalf("期望值 %q，实际 %q", want, string(res.Value))
	}
}

func oracleValue(res *Result) string {
	if !res.Exists {
		return "<不存在>"
	}
	return fmt.Sprintf("%q", string(res.Value))
}

// 快照点恰好等于某次写入：快照基必须包含该次写入；
// 之后的增量与快照内容交叠合并（同键覆盖、新键追加），最新写胜出。
func TestSnapshotBoundaryEqualsWrite(t *testing.T) {
	s := New()

	mustWrite := func(key, val string) int {
		seq, err := s.Write(key, []byte(val))
		if err != nil {
			t.Fatalf("写入 %s=%s 失败: %v", key, val, err)
		}
		t.Logf("[write] input={key:%q, value:%q}, 返回 seq=%d", key, val, seq)
		return seq
	}

	mustWrite("a", "v1")
	mustWrite("b", "v1")
	mustWrite("a", "v2")

	snapSeq := s.Snapshot()
	t.Logf("[snapshot] 冻结快照点 snapshotPoint=%d（恰好等于第 3 次写入）", snapSeq)
	if snapSeq != 3 {
		t.Fatalf("期望快照点为 3，实际为 %d", snapSeq)
	}

	// 读位点等于快照点：只取快照基，增量区间 (snap, seq] 为空。
	res, err := readAndLog(t, "at-snapshot", s, 3, "a")
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, res, "v2")

	// 读位点早于快照点：整体拒绝。
	if _, err = readAndLog(t, "before-snapshot", s, 2, "a"); !errors.Is(err, ErrSeqBeforeSnapshot) {
		t.Fatalf("期望 ErrSeqBeforeSnapshot，实际 %v", err)
	}

	// 快照之后写入，与快照内容交叠。
	mustWrite("a", "v3")
	mustWrite("c", "v1")
	mustWrite("a", "v4")

	res, err = readAndLog(t, "after-snapshot", s, 6, "a")
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, res, "v4")

	res, err = readAndLog(t, "incremental-start", s, 4, "a")
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, res, "v3")

	res, err = readAndLog(t, "new-key", s, 5, "c")
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, res, "v1")

	// 快照中存在、增量区间内未出现的键仍取快照基。
	res, err = readAndLog(t, "base-only", s, 6, "b")
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, res, "v1")

	// 从未写过的键：不存在而非错误。
	res, err = readAndLog(t, "missing", s, 6, "ghost")
	if err != nil {
		t.Fatal(err)
	}
	if res.Exists {
		t.Fatalf("期望 ghost 不存在，实际返回 %q", res.Value)
	}

	// 日志不因快照清空：6 次写入全部保留。
	if s.LogLen() != 6 {
		t.Fatalf("期望日志长度 6，实际 %d", s.LogLen())
	}
}

// 反复快照：快照点持续前移，并在所有当前可读位点上
// 用 ReplayFromZero 从头重放逐字段核对。
func TestRepeatedSnapshotsMatchReplay(t *testing.T) {
	s := New()

	write := func(key, val string) {
		t.Helper()
		if _, err := s.Write(key, []byte(val)); err != nil {
			t.Fatal(err)
		}
	}

	var snaps []int
	write("k", "w1")
	write("x", "w1")
	snaps = append(snaps, s.Snapshot()) // 2：快照点恰好等于写入
	write("k", "w2")
	snaps = append(snaps, s.Snapshot()) // 3
	write("k", "w3")
	write("x", "w2")
	snaps = append(snaps, s.Snapshot()) // 5
	write("k", "w4")

	snap := s.SnapshotSeq()
	t.Logf("[setup] 当前快照点=%d, 历史快照=%v, 当前位点=%d", snap, snaps, s.Seq())

	keys := []string{"k", "x", "missing"}
	for seq := snap; seq <= s.Seq(); seq++ {
		for _, key := range keys {
			got, err := s.ReadAt(seq, key)
			if err != nil {
				t.Fatalf("ReadAt(%d,%q) 意外失败: %v", seq, key, err)
			}
			want, err := s.ReplayFromZero(seq, key)
			if err != nil {
				t.Fatalf("ReplayFromZero(%d,%q) 失败: %v", seq, key, err)
			}
			logCase(t, "dual-read", key, seq, snap, got, nil)
			t.Logf("[oracle] input={key:%q, seq:%d}, return=%v, basis=%s",
				key, seq, oracleValue(want), want.Basis)
			if got.Exists != want.Exists || !bytes.Equal(got.Value, want.Value) {
				t.Fatalf("seq=%d key=%q 双读=%v 重放=%v，不一致",
					seq, key, oracleValue(got), oracleValue(want))
			}
		}
	}

	// 旧快照点之下的位点在新快照冻结后必须拒绝。
	if _, err := readAndLog(t, "stale-seq", s, snaps[0]-1, "k"); !errors.Is(err, ErrSeqBeforeSnapshot) {
		t.Fatalf("位点 %d 期望 ErrSeqBeforeSnapshot，实际 %v", snaps[0]-1, err)
	}
}

// 所有拒绝路径都不得改变日志、快照点与快照内容。
func TestRejectionsLeaveStateUntouched(t *testing.T) {
	s := New()
	if _, err := s.Write("a", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	snapSeq := s.Snapshot()
	if _, err := s.Write("a", []byte("v2")); err != nil {
		t.Fatal(err)
	}

	beforeSeq, beforeSnap, beforeLog := s.Seq(), s.SnapshotSeq(), s.LogLen()

	reject := func(name string, fn func() error) {
		t.Helper()
		err := fn()
		if err == nil {
			t.Fatalf("%s: 期望失败，实际成功", name)
		}
		if !errors.Is(err, ErrEmptyKey) && !errors.Is(err, ErrEmptyValue) &&
			!errors.Is(err, ErrSeqBeforeSnapshot) && !errors.Is(err, ErrSeqInFuture) {
			t.Fatalf("%s: 返回了未预期的错误 %v", name, err)
		}
		t.Logf("[reject:%s] input 拒绝, err=%v, 状态快照={seq:%d, snapshot:%d, logLen:%d}",
			name, err, s.Seq(), s.SnapshotSeq(), s.LogLen())
	}

	reject("empty-key-write", func() error {
		_, err := s.Write("", []byte("x"))
		return err
	})
	reject("empty-value-write", func() error {
		_, err := s.Write("a", nil)
		return err
	})
	reject("zero-len-value-write", func() error {
		_, err := s.Write("a", []byte{})
		return err
	})
	reject("empty-key-read", func() error {
		_, err := s.ReadAt(beforeSnap, "")
		return err
	})
	reject("read-before-snapshot", func() error {
		_, err := s.ReadAt(snapSeq-1, "a")
		return err
	})
	reject("read-future", func() error {
		_, err := s.ReadAt(beforeSeq+1, "a")
		return err
	})

	if s.Seq() != beforeSeq || s.SnapshotSeq() != beforeSnap || s.LogLen() != beforeLog {
		t.Fatalf("失败调用改变了状态: before={%d,%d,%d} after={%d,%d,%d}",
			beforeSeq, beforeSnap, beforeLog, s.Seq(), s.SnapshotSeq(), s.LogLen())
	}

	// 失败后正常读仍可复现，且与全量重放一致。
	res, err := s.ReadAt(beforeSeq, "a")
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, res, "v2")
}

// 无快照时也必须可回答：区间 [1, seq] 全部走增量扫描。
func TestReadWithoutSnapshot(t *testing.T) {
	s := New()
	if _, err := s.Write("a", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write("a", []byte("v2")); err != nil {
		t.Fatal(err)
	}
	res, err := readAndLog(t, "no-snapshot", s, 2, "a")
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, res, "v2")
}

// 写入与反复快照并发进行时，读取必须始终给出与全量重放一致的结果，
// 不出现中间态。
func TestConcurrentReadersDuringWritesAndSnapshots(t *testing.T) {
	s := New()
	for i := 0; i < 5; i++ {
		if _, err := s.Write("k", []byte(fmt.Sprintf("seed-%d", i))); err != nil {
			t.Fatal(err)
		}
	}

	var writers sync.WaitGroup
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(id int) {
			defer writers.Done()
			for i := 0; i < 25; i++ {
				if _, err := s.Write("k", []byte(fmt.Sprintf("w%d-%d", id, i))); err != nil {
					t.Errorf("并发写入失败: %v", err)
					return
				}
			}
		}(w)
	}

	var snappers sync.WaitGroup
	snappers.Add(1)
	go func() {
		defer snappers.Done()
		for i := 0; i < 20; i++ {
			s.Snapshot()
		}
	}()

	var readers sync.WaitGroup
	for p := 0; p < 40; p++ {
		readers.Add(2)
		for g := 0; g < 2; g++ {
			go func() {
				defer readers.Done()
				for attempt := 0; attempt < 200; attempt++ {
					snapSeq := s.SnapshotSeq()
					head := s.Seq()
					if head < snapSeq || head < 5 {
						continue
					}
					seq := snapSeq + attempt%(head-snapSeq+1)
					if seq == 0 {
						continue
					}

					res, err := s.ReadAt(seq, "k")
					if errors.Is(err, ErrSeqBeforeSnapshot) || errors.Is(err, ErrSeqInFuture) {
						// 两次观察之间快照点或位点推进，属合法竞争，重试。
						continue
					}
					if err != nil {
						t.Errorf("并发读意外失败: %v", err)
						return
					}

					oracle, err := s.ReplayFromZero(seq, "k")
					if err != nil {
						t.Errorf("重放核对失败: %v", err)
						return
					}
					if !res.Exists || !bytes.Equal(res.Value, oracle.Value) {
						t.Errorf("并发读 seq=%d 与重放不一致: %q vs %v",
							seq, res.Value, oracleValue(oracle))
						return
					}
				}
			}()
		}
	}

	readers.Wait()
	writers.Wait()
	snappers.Wait()

	// 收尾：冻结最终快照后，全部可读位点逐一与重放核对。
	s.Snapshot()
	snapSeq := s.SnapshotSeq()
	for seq := snapSeq; seq <= s.Seq(); seq++ {
		got, err := s.ReadAt(seq, "k")
		if err != nil {
			t.Fatalf("收尾核对 seq=%d 失败: %v", seq, err)
		}
		want, err := s.ReplayFromZero(seq, "k")
		if err != nil {
			t.Fatal(err)
		}
		logCase(t, "final-check", "k", seq, snapSeq, got, nil)
		if !bytes.Equal(got.Value, want.Value) {
			t.Fatalf("收尾核对 seq=%d 不一致: %q vs %q",
				seq, got.Value, want.Value)
		}
	}
}
