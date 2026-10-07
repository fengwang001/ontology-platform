package snapshot

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// 多路并发写入与多路并发快照读取同时进行。每个读会话的结果必须
// 满足以下可串行化见证条件（等价于所有请求按某一全局顺序串行处理）：
//  1. 快照恰好等于日志在边界位置的前缀重放结果；
//  2. 增量记录从 boundary+1 开始连续、按接受顺序、每条承载一笔完整事务；
//  3. 快照与增量拼接后的状态等于日志在已输出最大 LSN 处的前缀重放结果；
//  4. 拼接后的每个前缀状态都满足引用完整性。
func TestConcurrentLinearizable(t *testing.T) {
	j := NewJournal()
	coord := NewCoordinator(j)

	const writers = 4
	const txnsPerWriter = 50
	const readers = 3
	const exportsPerReader = 30

	var wg sync.WaitGroup
	errCh := make(chan error, writers*txnsPerWriter+readers*exportsPerReader)

	// 写入方：每笔事务自包含（对象与引用它们的链接同事务），
	// 保证日志始终满足引用完整性与事务连续性。
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < txnsPerWriter; i++ {
				txn := TxnID(fmt.Sprintf("w%d-t%d", w, i))
				a := fmt.Sprintf("w%d-o%d-a", w, i)
				b := fmt.Sprintf("w%d-o%d-b", w, i)
				j.AppendTransaction(txn, []WriteSpec{
					objSpec("", a),
					objSpec("", b),
					linkSpec("", fmt.Sprintf("w%d-l%d", w, i), a, b),
					actSpec("", fmt.Sprintf("w%d-act%d", w, i)),
				})
			}
		}(w)
	}

	// 读取方：并发开启导出会话并验证可串行化见证条件。
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for i := 0; i < exportsPerReader; i++ {
				sess, err := coord.BeginExport(Auto(), Options{})
				if err != nil {
					errCh <- fmt.Errorf("reader %d: begin: %w", r, err)
					return
				}
				state, err := sess.Snapshot()
				if err != nil {
					errCh <- fmt.Errorf("reader %d: snapshot: %w", r, err)
					return
				}
				incrs, err := sess.Drain()
				if err != nil {
					errCh <- fmt.Errorf("reader %d: drain: %w", r, err)
					return
				}
				if err := verifySession(j, sess.Boundary(), state, incrs); err != nil {
					errCh <- fmt.Errorf("reader %d round %d: %w", r, i, err)
					return
				}
			}
		}(r)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

// verifySession 校验一次导出会话结果的可串行化见证条件。
func verifySession(j *Journal, boundary LSN, state State, incrs []Increment) error {
	// 条件 1：快照 == 边界前缀的朴素重放。
	wantSnap := NewState()
	for _, w := range j.Entries(0, boundary) {
		wantSnap.Apply(w)
	}
	if !reflect.DeepEqual(state, wantSnap) {
		return fmt.Errorf("snapshot != replay of prefix [0,%d]", boundary)
	}
	// 条件 2：增量连续、有序、与日志内容一致。
	next := boundary + 1
	stitched := state
	for _, incr := range incrs {
		if incr.FromLSN != next {
			return fmt.Errorf("increment gap: expected FromLSN %d, got %d", next, incr.FromLSN)
		}
		for _, w := range incr.Writes {
			if w.Txn != incr.Txn {
				return fmt.Errorf("increment %s carries foreign txn write at LSN %d", incr.Txn, w.LSN)
			}
			got := j.Entries(w.LSN-1, w.LSN)
			if len(got) != 1 || !reflect.DeepEqual(got[0], w) {
				return fmt.Errorf("increment write at LSN %d does not match journal", w.LSN)
			}
			stitched.Apply(w)
		}
		next = incr.ToLSN + 1
		// 条件 4：每个前缀状态下引用完整性成立。
		for id, l := range stitched.Links {
			if _, ok := stitched.Objects[l.Src]; !ok {
				return fmt.Errorf("link %s visible without src %s after LSN %d", id, l.Src, incr.ToLSN)
			}
			if _, ok := stitched.Objects[l.Dst]; !ok {
				return fmt.Errorf("link %s visible without dst %s after LSN %d", id, l.Dst, incr.ToLSN)
			}
		}
	}
	// 条件 3：拼接结果 == 已输出最大 LSN 处的前缀重放。
	wantFinal := NewState()
	last := boundary
	if len(incrs) > 0 {
		last = incrs[len(incrs)-1].ToLSN
	}
	for _, w := range j.Entries(0, last) {
		wantFinal.Apply(w)
	}
	if !reflect.DeepEqual(stitched, wantFinal) {
		return fmt.Errorf("stitched state != replay of prefix [0,%d]", last)
	}
	return nil
}
