package fkjoin_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/fkjoin"
)

func strptr(s string) *string { return &s }

// driver 模拟右表侧：订阅变化或右表变化时，为订阅者产生响应并入队。
// 响应携带产生时刻的右表值与左行哈希，按产生顺序进入 FIFO 队列。
type driver struct {
	j *fkjoin.Joiner
}

func (d driver) poke(key string) {
	rv, ok := d.j.RightValue(key)
	if !ok {
		return
	}
	subs := d.j.Lookup(key)
	ids := make([]string, 0, len(subs))
	for id := range subs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := d.j.Enqueue(id, key, rv, subs[id]); err != nil {
			panic(err)
		}
	}
}

func (d driver) upsertLeft(id string, row fkjoin.Row) {
	if err := d.j.UpsertLeft(id, row); err != nil {
		panic(err)
	}
	if row.FK != nil {
		d.poke(*row.FK)
	}
}

func (d driver) upsertRight(key, value string) {
	if err := d.j.UpsertRight(key, value); err != nil {
		panic(err)
	}
	d.poke(key)
}

func (d driver) deleteRight(key string) {
	if err := d.j.DeleteRight(key); err != nil {
		panic(err)
	}
}

func assertNaive(t *testing.T, j *fkjoin.Joiner) {
	t.Helper()
	got := j.Results()
	want := fkjoin.NaiveJoin(j.Left(), j.Right())
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("results != naive join\n got: %v\nwant: %v", got, want)
	}
}

// 哈希校验丢弃过期响应：响应入队后左行被修改，投递时哈希不一致。
func TestStaleResponseDiscardedByHash(t *testing.T) {
	var logBuf bytes.Buffer
	j := fkjoin.New(16, &logBuf)
	d := driver{j}

	d.upsertRight("k1", "R1")
	d.upsertLeft("L1", fkjoin.Row{FK: strptr("k1"), Value: "A"})
	if j.Pending() != 1 {
		t.Fatalf("pending = %d, want 1", j.Pending())
	}

	// 响应尚在队列中，左行值被修改，左行哈希随之改变。
	if err := j.UpsertLeft("L1", fkjoin.Row{FK: strptr("k1"), Value: "A2"}); err != nil {
		t.Fatal(err)
	}

	d1, err := j.Deliver()
	if err != nil {
		t.Fatal(err)
	}
	if d1.Applied || !d1.Stale {
		t.Fatalf("delivery = %+v, want stale discard", d1)
	}
	if j.Discarded() != 1 {
		t.Fatalf("discarded = %d, want 1", j.Discarded())
	}
	if got := j.Results(); len(got) != 0 {
		t.Fatalf("results = %v, want empty", got)
	}
	if !strings.Contains(logBuf.String(), "hash mismatch") {
		t.Fatalf("log missing evidence of hash mismatch:\n%s", logBuf.String())
	}

	// 右表侧按当前左行重新产生响应，投递后结果恢复且与朴素连接一致。
	d.poke("k1")
	j.Drain()
	assertNaive(t, j)
	got := j.Results()
	if len(got) != 1 || got[0].Left != "A2" || got[0].Right != "R1" {
		t.Fatalf("results = %v, want [{L1 k1 A2 R1}]", got)
	}
	t.Logf("component log:\n%s", logBuf.String())
}

// state 快照用于验证被拒绝的操作不产生任何副作用。
type state struct {
	left      map[string]fkjoin.Row
	right     map[string]string
	subs      map[string]string
	results   []fkjoin.Entry
	discarded uint64
	pending   int
}

func snapshot(j *fkjoin.Joiner) state {
	return state{
		left:      j.Left(),
		right:     j.Right(),
		subs:      j.Subscriptions(),
		results:   j.Results(),
		discarded: j.Discarded(),
		pending:   j.Pending(),
	}
}

// 各类非法输入：空键、空队列投递、待投递响应超限。
// 每个用例验证拒绝原因可区分，且两表、订阅、队列、结果、丢弃数均不变。
func TestInvalidInputsRejected(t *testing.T) {
	var logBuf bytes.Buffer
	j := fkjoin.New(2, &logBuf)
	d := driver{j}

	d.upsertRight("k1", "R1")
	d.upsertLeft("L1", fkjoin.Row{FK: strptr("k1"), Value: "A"})
	j.Drain()
	before := snapshot(j)

	cases := []struct {
		name   string
		op     string
		run    func() error
		reason error
	}{
		{"upsert left with empty fk", "UpsertLeft", func() error {
			return j.UpsertLeft("L2", fkjoin.Row{FK: strptr(""), Value: "X"})
		}, fkjoin.ErrEmptyKey},
		{"upsert right with empty key", "UpsertRight", func() error {
			return j.UpsertRight("", "X")
		}, fkjoin.ErrEmptyKey},
		{"delete right with empty key", "DeleteRight", func() error {
			return j.DeleteRight("")
		}, fkjoin.ErrEmptyKey},
		{"enqueue with empty key", "Enqueue", func() error {
			return j.Enqueue("L1", "", "X", 0)
		}, fkjoin.ErrEmptyKey},
		{"deliver on empty queue", "Deliver", func() error {
			_, err := j.Deliver()
			return err
		}, fkjoin.ErrEmptyQueue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("expected rejection, got nil error")
			}
			if !errors.Is(err, tc.reason) {
				t.Fatalf("error = %v, want reason %v", err, tc.reason)
			}
			var re *fkjoin.RejectError
			if !errors.As(err, &re) || re.Op != tc.op {
				t.Fatalf("error = %v, want RejectError{Op: %s}", err, tc.op)
			}
			if got, want := snapshot(j), before; !reflect.DeepEqual(got, want) {
				t.Fatalf("state changed after rejection\n got: %+v\nwant: %+v", got, want)
			}
		})
	}

	// 待投递响应超限：上限为 2，第 3 次入队被拒绝。
	if err := j.Enqueue("L1", "k1", "R1", fkjoin.HashRow("L1", fkjoin.Row{FK: strptr("k1"), Value: "A"})); err != nil {
		t.Fatal(err)
	}
	if err := j.Enqueue("L1", "k1", "R1", uint64(0)); err != nil {
		t.Fatal(err)
	}
	fullState := snapshot(j)
	err := j.Enqueue("L1", "k1", "R1", uint64(0))
	if !errors.Is(err, fkjoin.ErrPendingLimit) {
		t.Fatalf("error = %v, want ErrPendingLimit", err)
	}
	if got := snapshot(j); !reflect.DeepEqual(got, fullState) {
		t.Fatalf("state changed after limit rejection\n got: %+v\nwant: %+v", got, fullState)
	}
	j.Drain()
	assertNaive(t, j)
	if !strings.Contains(logBuf.String(), "REJECT") {
		t.Fatalf("log missing rejection records:\n%s", logBuf.String())
	}
	t.Logf("component log:\n%s", logBuf.String())
}

// 空键外键与空字符串外键的区分：nil 外键合法（不订阅），空字符串外键非法。
func TestNilVsEmptyForeignKey(t *testing.T) {
	j := fkjoin.New(16, nil)
	if err := j.UpsertLeft("L1", fkjoin.Row{FK: nil, Value: "A"}); err != nil {
		t.Fatalf("nil FK must be accepted: %v", err)
	}
	if err := j.UpsertLeft("L2", fkjoin.Row{FK: strptr(""), Value: "B"}); !errors.Is(err, fkjoin.ErrEmptyKey) {
		t.Fatalf("empty-string FK must be rejected: %v", err)
	}
	if got := j.Subscriptions(); len(got) != 0 {
		t.Fatalf("subscriptions = %v, want empty", got)
	}
	assertNaive(t, j)
}

// 交错变更与乱序响应：以固定种子的伪随机序列交错执行左右表变更、
// 响应入队与部分投递，期间不断产生过期响应；排空全部响应后，
// 结果视图必须与朴素内连接完全一致。
func TestInterleavedChangesWithStaleResponses(t *testing.T) {
	var logBuf bytes.Buffer
	j := fkjoin.New(1<<16, &logBuf)
	d := driver{j}
	rng := rand.New(rand.NewSource(42))

	keys := []string{"k1", "k2", "k3"}
	leftIDs := []string{"L1", "L2", "L3", "L4", "L5"}
	step := 0
	for i := 0; i < 600; i++ {
		step++
		switch rng.Intn(7) {
		case 0, 1: // 左表插入/更新，外键随机（含为空）
			id := leftIDs[rng.Intn(len(leftIDs))]
			row := fkjoin.Row{Value: fmt.Sprintf("v%d", step)}
			if rng.Intn(4) != 0 {
				row.FK = strptr(keys[rng.Intn(len(keys))])
			}
			d.upsertLeft(id, row)
		case 2: // 左表删除
			j.DeleteLeft(leftIDs[rng.Intn(len(leftIDs))])
		case 3, 4: // 右表插入/更新
			d.upsertRight(keys[rng.Intn(len(keys))], fmt.Sprintf("R%d", step))
		case 5: // 右表删除
			d.deleteRight(keys[rng.Intn(len(keys))])
		case 6: // 部分投递，制造在途响应与后续变更交错
			for n := rng.Intn(3); n > 0 && j.Pending() > 0; n-- {
				if _, err := j.Deliver(); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	j.Drain()
	if j.Pending() != 0 {
		t.Fatalf("pending = %d, want 0 after drain", j.Pending())
	}
	if j.Discarded() == 0 {
		t.Fatal("scenario must have exercised stale-response discards")
	}
	assertNaive(t, j)
	t.Logf("discarded=%d results=%v", j.Discarded(), j.Results())
}

// 响应按产生顺序投递：FIFO。
func TestResponsesDeliveredInFIFOOrder(t *testing.T) {
	j := fkjoin.New(16, nil)
	row := fkjoin.Row{FK: strptr("k1"), Value: "A"}
	if err := j.UpsertLeft("L1", row); err != nil {
		t.Fatal(err)
	}
	if err := j.UpsertRight("k1", "R1"); err != nil {
		t.Fatal(err)
	}
	hash := fkjoin.HashRow("L1", row)
	for _, v := range []string{"R1", "R1", "R1"} {
		if err := j.Enqueue("L1", "k1", v, hash); err != nil {
			t.Fatal(err)
		}
	}
	deliveries := j.Drain()
	if len(deliveries) != 3 {
		t.Fatalf("deliveries = %d, want 3", len(deliveries))
	}
	for i, dl := range deliveries {
		if dl.Resp.Seq != uint64(i+1) {
			t.Fatalf("delivery %d has seq %d, want %d (FIFO violated)", i, dl.Resp.Seq, i+1)
		}
	}
}

// 结果可被并发读取且逐条一致：读取期间写入持续进行，
// 每个快照内部必须有序、无重复、条目完整；竞态由 -race 检测。
func TestConcurrentReadsConsistent(t *testing.T) {
	j := fkjoin.New(1<<16, nil)
	d := driver{j}
	keys := []string{"k1", "k2", "k3"}

	var writers, readers sync.WaitGroup
	stop := make(chan struct{})

	// 写入协程：交错变更左右表并不定期投递。
	writers.Add(1)
	go func() {
		defer writers.Done()
		rng := rand.New(rand.NewSource(7))
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if j.Pending() > 4096 {
				j.Drain()
			}
			id := fmt.Sprintf("L%d", rng.Intn(5))
			key := keys[rng.Intn(len(keys))]
			switch rng.Intn(4) {
			case 0:
				d.upsertLeft(id, fkjoin.Row{FK: strptr(key), Value: fmt.Sprintf("v%d", i)})
			case 1:
				d.upsertRight(key, fmt.Sprintf("R%d", i))
			case 2:
				j.DeleteLeft(id)
			case 3:
				for n := rng.Intn(4); n > 0 && j.Pending() > 0; n-- {
					if _, err := j.Deliver(); err != nil {
						return
					}
				}
			}
		}
	}()

	// 读取协程：校验每个结果快照逐条一致。
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 2000; i++ {
				entries := j.Results()
				for k, e := range entries {
					if e.LeftID == "" || e.Key == "" {
						t.Errorf("malformed entry: %+v", e)
					}
					if k > 0 && entries[k-1].LeftID >= e.LeftID {
						t.Errorf("snapshot not sorted/unique: %v", entries)
					}
				}
				_ = j.Discarded()
				_ = j.Pending()
				_ = j.Subscriptions()
			}
		}()
	}

	readers.Wait()
	close(stop)
	writers.Wait()
}

// 确定性：同一输入序列反复计算，结果、丢弃数与日志输出完全相同。
func TestDeterministicReplay(t *testing.T) {
	run := func() ([]fkjoin.Entry, uint64, string) {
		var logBuf bytes.Buffer
		j := fkjoin.New(64, &logBuf)
		d := driver{j}
		rng := rand.New(rand.NewSource(99))
		keys := []string{"k1", "k2", "k3"}
		for i := 0; i < 200; i++ {
			id := fmt.Sprintf("L%d", rng.Intn(4))
			key := keys[rng.Intn(len(keys))]
			switch rng.Intn(6) {
			case 0:
				d.upsertLeft(id, fkjoin.Row{FK: strptr(key), Value: fmt.Sprintf("v%d", i)})
			case 1:
				d.upsertLeft(id, fkjoin.Row{FK: nil, Value: fmt.Sprintf("v%d", i)})
			case 2:
				j.DeleteLeft(id)
			case 3:
				d.upsertRight(key, fmt.Sprintf("R%d", i))
			case 4:
				d.deleteRight(key)
			case 5:
				j.Drain()
			}
		}
		j.Drain()
		return j.Results(), j.Discarded(), logBuf.String()
	}

	res1, disc1, log1 := run()
	for i := 0; i < 3; i++ {
		res2, disc2, log2 := run()
		if !reflect.DeepEqual(res1, res2) {
			t.Fatalf("run %d: results differ\n%v\n%v", i, res1, res2)
		}
		if disc1 != disc2 {
			t.Fatalf("run %d: discarded %d != %d", i, disc1, disc2)
		}
		if log1 != log2 {
			t.Fatalf("run %d: logs differ", i)
		}
	}
}

// 外键改为空：订阅撤销、结果立即撤回，已在队列中的旧响应被丢弃。
func TestForeignKeySetToNull(t *testing.T) {
	var logBuf bytes.Buffer
	j := fkjoin.New(16, &logBuf)
	d := driver{j}

	d.upsertRight("k1", "R1")
	d.upsertLeft("L1", fkjoin.Row{FK: strptr("k1"), Value: "A"})
	j.Drain()
	if got := j.Results(); len(got) != 1 {
		t.Fatalf("results = %v, want 1 entry", got)
	}

	// 产生一条尚未投递的响应，然后将外键置空。
	d.poke("k1")
	if err := j.UpsertLeft("L1", fkjoin.Row{FK: nil, Value: "A"}); err != nil {
		t.Fatal(err)
	}
	if got := j.Results(); len(got) != 0 {
		t.Fatalf("results = %v, want empty after FK nulled", got)
	}
	if got := j.Subscriptions(); len(got) != 0 {
		t.Fatalf("subscriptions = %v, want empty after FK nulled", got)
	}

	// 旧响应因哈希不一致被丢弃。
	d1, err := j.Deliver()
	if err != nil {
		t.Fatal(err)
	}
	if !d1.Stale || d1.Applied {
		t.Fatalf("delivery = %+v, want stale discard", d1)
	}
	if j.Discarded() != 1 {
		t.Fatalf("discarded = %d, want 1", j.Discarded())
	}
	assertNaive(t, j)
	t.Logf("component log:\n%s", logBuf.String())
}

// 删除右表键：引用该键的结果立即撤回；在途响应投递时因右表键缺失被丢弃；
// 右表键恢复后结果经响应确认重新出现。
func TestDeleteRightWithdrawsResult(t *testing.T) {
	var logBuf bytes.Buffer
	j := fkjoin.New(16, &logBuf)
	d := driver{j}

	d.upsertRight("k1", "R1")
	d.upsertLeft("L1", fkjoin.Row{FK: strptr("k1"), Value: "A"})
	d.upsertLeft("L2", fkjoin.Row{FK: strptr("k1"), Value: "B"})
	j.Drain()
	if got := j.Results(); len(got) != 2 {
		t.Fatalf("results = %v, want 2 entries", got)
	}

	// 删除前再产生一批在途响应。
	d.poke("k1")
	d.deleteRight("k1")
	if got := j.Results(); len(got) != 0 {
		t.Fatalf("results = %v, want empty after right key deleted", got)
	}

	// 在途响应投递时右表键已缺失，全部丢弃。
	deliveries := j.Drain()
	if len(deliveries) != 2 {
		t.Fatalf("deliveries = %d, want 2", len(deliveries))
	}
	for _, dl := range deliveries {
		if !dl.Stale || dl.Applied {
			t.Fatalf("delivery = %+v, want stale discard", dl)
		}
	}
	if j.Discarded() != 2 {
		t.Fatalf("discarded = %d, want 2", j.Discarded())
	}
	assertNaive(t, j)

	// 订阅仍然保留：右表键恢复后结果重新出现。
	d.upsertRight("k1", "R2")
	j.Drain()
	assertNaive(t, j)
	got := j.Results()
	if len(got) != 2 || got[0].Right != "R2" || got[1].Right != "R2" {
		t.Fatalf("results = %v, want 2 entries with R2", got)
	}
	t.Logf("component log:\n%s", logBuf.String())
}
