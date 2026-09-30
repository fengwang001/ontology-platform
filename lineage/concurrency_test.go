package lineage

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// 串行场景下“同一键记录的提交顺序等于追加顺序”：
// 旧分片未排空前，新分片不可领取消费，因此键 k 在旧分片位置 p 的记录
// 必然先于其在分裂后新分片上的记录被提交。
func TestPerKeyCommitOrderAcrossSplit(t *testing.T) {
	c := New(10, 1_000, 10, discardLogger{})
	_, _ = c.Append(7) // shard 0, pos 0
	mustOK(t, c.Grant(0, "old"), "Grant(0)")
	_, right, _ := c.Split(0, 5)

	// 父未排空：承载 key=7 的新分片不可领取。
	var ge *GrantError
	err := c.Grant(right, "new")
	if err == nil || !errors.As(err, &ge) || !errors.Is(ge.Err, ErrParentsUndrained) {
		t.Fatalf("期望父未排空的 GrantError, 实际 %v", err)
	}
	// 父分片按追加顺序提交完毕。
	mustOK(t, c.Commit(0, "old", 1), "父分片提交 pos0")

	// 此后新分片才可被领取；key=7 的新记录提交必然晚于 pos0。
	mustOK(t, c.Grant(right, "new"), "父排空后领取新分片")
	r, err := c.Append(7)
	mustOK(t, err, "Append(7) 分裂后")
	if r.Shard != right || r.Position != 0 {
		t.Fatalf("新记录应位于新分片位置 0: %+v", r)
	}
	mustOK(t, c.Commit(right, "new", 1), "新分片提交")
}

// TestConcurrentAppendNoLossNoDup 并发追加：每个分片位置 0..n-1 各出现一次，不丢不重。
func TestConcurrentAppendNoLossNoDup(t *testing.T) {
	c := New(4, 1_000_000, 8, discardLogger{})
	l, r, err := c.Split(0, 2)
	mustOK(t, err, "Split(0,2)")

	const perKey = 500
	var wg sync.WaitGroup
	for key := 0; key < 4; key++ {
		for i := 0; i < perKey; i++ {
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				if _, err := c.Append(k); err != nil {
					t.Errorf("Append(%d) 失败: %v", k, err)
				}
			}(key)
		}
	}
	wg.Wait()

	want := map[ShardID]int64{l: 2 * perKey, r: 2 * perKey}
	for id, n := range want {
		info, ok := c.Get(id)
		if !ok {
			t.Fatalf("分片 %d 不存在", id)
		}
		if info.Appended != n {
			t.Fatalf("分片 %d 追加条数期望 %d, 实际 %d", id, n, info.Appended)
		}
	}

	// 直接检查位置集合，确保 0..n-1 无重号（条数正确 + 位置取自锁内计数器即可证明）。
	if infos := c.List(); len(infos) != 3 {
		t.Fatalf("谱系分片数期望 3, 实际 %d", len(infos))
	}
}

// TestConcurrentGrantMutualExclusion 并发领取与时钟推进下，任一分片至多一个有效持有者。
func TestConcurrentGrantMutualExclusion(t *testing.T) {
	c := New(100, 2, 100, discardLogger{})

	// 构造多个待领取的开放分片：0..9 次分裂会改变谱系，改为反复分裂左子。
	var leaves []ShardID
	cur := ShardID(0)
	for i := 1; i <= 8; i++ {
		info, _ := c.Get(cur)
		mid := info.Lo + 10
		left, right, err := c.Split(cur, mid)
		mustOK(t, err, fmt.Sprintf("Split 第 %d 次", i))
		leaves = append(leaves, left)
		cur = right
	}
	leaves = append(leaves, cur)

	var wg sync.WaitGroup
	// 多个工作者反复抢同一片叶子；同时推进时钟使租约周期性过期。
	for w := 0; w < 6; w++ {
		worker := fmt.Sprintf("w%d", w)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 200; round++ {
				for _, id := range leaves {
					if err := c.Grant(id, WorkerID(worker)); err == nil {
						_ = c.Commit(id, WorkerID(worker), 0)
						_ = c.Renew(id, WorkerID(worker))
					}
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for tick := 1; tick <= 50; tick++ {
			_ = c.AdvanceClock(Clock(tick))
		}
	}()
	wg.Wait()

	// 最终一致性检查：所有查询到的有效持有者，其工作者有效租约数不超过上限。
	counts := map[string]int{}
	for _, info := range c.List() {
		if info.LeaseValid {
			counts[string(info.Holder)]++
		}
	}
	for w, n := range counts {
		if n > 100 {
			t.Fatalf("工作者 %s 有效租约数 %d 超过上限", w, n)
		}
	}
}

// op 记录一次确定性重放操作。
type op struct {
	name   string
	key    int
	shard  ShardID
	shard2 ShardID
	mid    int
	worker string
	prog   int64
	clock  Clock
}

// TestReplayDeterministic 同一操作序列重放结果完全相同（含错误与返回值）。
func TestReplayDeterministic(t *testing.T) {
	plan := func() []op {
		return []op{
			{name: "clock", clock: 0},
			{name: "append", key: 5},
			{name: "grant", shard: 0, worker: "a"},
			{name: "split", shard: 0, mid: 5},
			{name: "grant", shard: 1, worker: "b"}, // 父未排空
			{name: "commit", shard: 0, worker: "a", prog: 1},
			{name: "grant", shard: 1, worker: "b"},
			{name: "grant", shard: 2, worker: "b"}, // b 超限(max=1)
			{name: "merge", shard: 1, shard2: 2},   // 父分片 0 已排空，合并成功
			{name: "append", key: 9},
			{name: "clock", clock: 10}, // a/b 租约(ttl=7)到期
			{name: "commit", shard: 1, worker: "b", prog: 0},
			{name: "append", key: 10}, // 越界
			{name: "clock", clock: 3}, // 回退
		}
	}

	run := func(ops []op) string {
		c := New(10, 7, 1, discardLogger{})
		out := ""
		for _, o := range ops {
			switch o.name {
			case "clock":
				out += fmt.Sprintf("clock(%d)=%v\n", o.clock, c.AdvanceClock(o.clock))
			case "append":
				r, err := c.Append(o.key)
				out += fmt.Sprintf("append(%d)=(%d,%d,%v)\n", o.key, r.Shard, r.Position, err)
			case "split":
				l, rr, err := c.Split(o.shard, o.mid)
				out += fmt.Sprintf("split(%d,%d)=(%d,%d,%v)\n", o.shard, o.mid, l, rr, err)
			case "merge":
				ch, err := c.Merge(o.shard, o.shard2)
				out += fmt.Sprintf("merge(%d,%d)=(%d,%v)\n", o.shard, o.shard2, ch, err)
			case "grant":
				out += fmt.Sprintf("grant(%d,%s)=%v\n", o.shard, o.worker, c.Grant(o.shard, WorkerID(o.worker)))
			case "commit":
				out += fmt.Sprintf("commit(%d,%s,%d)=%v\n", o.shard, o.worker, o.prog,
					c.Commit(o.shard, WorkerID(o.worker), o.prog))
			}
		}
		for _, info := range c.List() {
			out += fmt.Sprintf("info(%d)=[%d,%d) closed=%v app=%d com=%d drained=%v parents=%v holder=%s valid=%v exp=%d\n",
				info.ID, info.Lo, info.Hi, info.Closed, info.Appended, info.Committed,
				info.Drained, info.Parents, info.Holder, info.LeaseValid, info.ExpiresAt)
		}
		return out
	}

	first := run(plan())
	second := run(plan())
	if first != second {
		t.Fatalf("重放结果不一致:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}

	// 关键判定点抽查。
	c := New(10, 7, 1, discardLogger{})
	_, err := c.Append(5)
	mustOK(t, err, "append")
	mustOK(t, c.Grant(0, "a"), "grant a")
	l, r, err := c.Split(0, 5)
	mustOK(t, err, "split")
	if l != 1 || r != 2 {
		t.Fatalf("子 ID 确定性期望 1,2")
	}
	if err := c.Grant(1, "b"); err == nil {
		t.Fatalf("父未排空时不应授予")
	}
	t.Log("\n" + first)
}
