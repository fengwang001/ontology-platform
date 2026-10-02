package scheduler

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func mustArrive(t *testing.T, s *Scheduler, reads, writes []string) (int, []int) {
	t.Helper()
	id, released, err := s.Arrive(reads, writes)
	if err != nil {
		t.Fatalf("Arrive(%v, %v) unexpected error: %v", reads, writes, err)
	}
	t.Logf("Arrive reads=%v writes=%v -> id=%d released=%v", reads, writes, id, released)
	return id, released
}

func mustComplete(t *testing.T, s *Scheduler, id int) []int {
	t.Helper()
	released, err := s.Complete(id)
	if err != nil {
		t.Fatalf("Complete(%d) unexpected error: %v", id, err)
	}
	t.Logf("Complete(%d) -> released=%v", id, released)
	return released
}

func requireReleased(t *testing.T, op string, got, want []int) {
	t.Helper()
	if len(want) == 0 {
		want = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s released=%v, want %v", op, got, want)
	}
}

func requireStatus(t *testing.T, s *Scheduler, id int, want Status) {
	t.Helper()
	got, ok := s.Status(id)
	if !ok {
		t.Fatalf("Status(%d) not found, want %v", id, want)
	}
	if got != want {
		t.Fatalf("Status(%d)=%v, want %v", id, got, want)
	}
}

func requireBlockers(t *testing.T, s *Scheduler, id int, want []int) {
	t.Helper()
	got, err := s.Blockers(id)
	if err != nil {
		t.Fatalf("Blockers(%d) unexpected error: %v", id, err)
	}
	if len(want) == 0 {
		want = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Blockers(%d)=%v, want %v", id, got, want)
	}
	t.Logf("Blockers(%d) -> %v", id, got)
}

// 读者在等待的写者之后到达，即使不与运行中的读者冲突，也须等待。
// T1 读 a（运行）；T2 写 a（等待，被 T1 阻塞）；T3 读 a 与 T1 读读兼容，
// 但与等待中的 T2 冲突，因此 T3 必须等待，不得越过 T2。
func TestReaderWaitsBehindWaitingWriter(t *testing.T) {
	s := New(1)

	_, rel := mustArrive(t, s, []string{"a"}, nil)
	requireReleased(t, "arrive T1", rel, []int{1})

	_, rel = mustArrive(t, s, nil, []string{"a"})
	requireReleased(t, "arrive T2", rel, nil)
	requireBlockers(t, s, 2, []int{1})

	_, rel = mustArrive(t, s, []string{"a"}, nil)
	requireReleased(t, "arrive T3", rel, nil)
	requireBlockers(t, s, 3, []int{2})
	requireStatus(t, s, 3, Waiting)
	t.Log("判定依据: T3 与运行中的 T1 读读兼容，但与等待中的写者 T2 冲突，故不得越过 T2")

	rel = mustComplete(t, s, 1)
	requireReleased(t, "complete T1", rel, []int{2})
	requireStatus(t, s, 3, Waiting)

	rel = mustComplete(t, s, 2)
	requireReleased(t, "complete T2", rel, []int{3})
}

// 被并发上限 K 挡下的更早事务，仍阻挡后来的冲突者。
// K=1：T1 写 x 占满运行名额；T2 写 y 与 T1 不冲突但被 K 挡下；
// T3 写 y 与等待中的 T2 冲突，必须等待。
func TestKLimitBlockedEarlierTxnStillBlocks(t *testing.T) {
	s := New(1)

	_, rel := mustArrive(t, s, nil, []string{"x"})
	requireReleased(t, "arrive T1", rel, []int{1})

	_, rel = mustArrive(t, s, nil, []string{"y"})
	requireReleased(t, "arrive T2", rel, nil)
	requireBlockers(t, s, 2, nil)
	t.Log("判定依据: T2 与 T1 不冲突，仅因运行数达 K=1 被挡下")

	_, rel = mustArrive(t, s, nil, []string{"y"})
	requireReleased(t, "arrive T3", rel, nil)
	requireBlockers(t, s, 3, []int{2})
	t.Log("判定依据: T3 与被 K 挡下的 T2 冲突，故不得放行")

	rel = mustComplete(t, s, 1)
	requireReleased(t, "complete T1", rel, []int{2})
	requireStatus(t, s, 3, Waiting)

	rel = mustComplete(t, s, 2)
	requireReleased(t, "complete T2", rel, []int{3})
}

// 取消等待者后阻挡解除。
func TestCancelWaitingUnblocks(t *testing.T) {
	s := New(1)

	_, rel := mustArrive(t, s, nil, []string{"x"})
	requireReleased(t, "arrive T1", rel, []int{1})

	_, rel = mustArrive(t, s, nil, []string{"y"})
	requireReleased(t, "arrive T2", rel, nil)

	_, rel = mustArrive(t, s, nil, []string{"y"})
	requireReleased(t, "arrive T3", rel, nil)
	requireBlockers(t, s, 3, []int{2})

	released, err := s.Cancel(2)
	if err != nil {
		t.Fatalf("Cancel(2) unexpected error: %v", err)
	}
	requireReleased(t, "cancel T2", released, nil)
	t.Log("判定依据: 取消 T2 后运行数仍达 K=1，T3 继续等待但阻塞者已消失")
	requireBlockers(t, s, 3, nil)
	if _, err := s.Blockers(2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Blockers(2) after cancel err=%v, want ErrNotFound", err)
	}

	rel = mustComplete(t, s, 1)
	requireReleased(t, "complete T1", rel, []int{3})
}

// 完成触发连锁放行。
// K=2：T1 写 a,b 运行；T2 读 a、T3 读 b 各与 T1 冲突而等待；
// T4 读 c 不冲突直接运行占满 K；T5 读 c 被 K 挡下。
// 完成 T1 放行 T2，完成 T2 放行 T3，完成 T3 放行 T5，形成连锁。
func TestCompleteTriggersChainRelease(t *testing.T) {
	s := New(2)

	_, rel := mustArrive(t, s, nil, []string{"a", "b"})
	requireReleased(t, "arrive T1", rel, []int{1})

	_, rel = mustArrive(t, s, []string{"a"}, nil)
	requireReleased(t, "arrive T2", rel, nil)

	_, rel = mustArrive(t, s, []string{"b"}, nil)
	requireReleased(t, "arrive T3", rel, nil)

	_, rel = mustArrive(t, s, []string{"c"}, nil)
	requireReleased(t, "arrive T4", rel, []int{4})

	_, rel = mustArrive(t, s, []string{"c"}, nil)
	requireReleased(t, "arrive T5", rel, nil)
	requireBlockers(t, s, 5, nil)
	t.Log("判定依据: T5 与任何更早未完成者都不冲突，仅因运行数达 K=2 被挡下")

	rel = mustComplete(t, s, 1)
	requireReleased(t, "complete T1", rel, []int{2})
	t.Log("判定依据: T1 完成后 T2 阻塞解除被放行并占满 K，T3 继续等待")

	rel = mustComplete(t, s, 2)
	requireReleased(t, "complete T2", rel, []int{3})

	rel = mustComplete(t, s, 3)
	requireReleased(t, "complete T3", rel, []int{5})
	t.Log("判定依据: 每次完成都触发下一次放行，形成连锁")
}

// 到达校验：读写集皆空优先于空串键，只报第一个；被拒绝的到达不改变状态。
func TestArriveValidation(t *testing.T) {
	s := New(2)

	if _, _, err := s.Arrive(nil, nil); !errors.Is(err, ErrEmptySets) {
		t.Fatalf("Arrive(nil, nil) err=%v, want ErrEmptySets", err)
	}
	if _, _, err := s.Arrive([]string{}, []string{}); !errors.Is(err, ErrEmptySets) {
		t.Fatalf("Arrive([], []) err=%v, want ErrEmptySets", err)
	}
	if _, _, err := s.Arrive([]string{""}, nil); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Arrive([\"\"], nil) err=%v, want ErrEmptyKey", err)
	}
	if _, _, err := s.Arrive(nil, []string{"k", ""}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Arrive(nil, [k \"\"]) err=%v, want ErrEmptyKey", err)
	}
	t.Log("判定依据: 四类非法到达均被拒绝，且不得占用编号")

	id, rel := mustArrive(t, s, []string{"a"}, nil)
	if id != 1 {
		t.Fatalf("first valid arrive id=%d, want 1 (被拒绝的到达不得改变状态)", id)
	}
	requireReleased(t, "arrive T1", rel, []int{1})
}

// 完成校验：编号不存在、已完成、并非运行中，按此顺序只报第一个。
func TestCompleteErrorPrecedence(t *testing.T) {
	s := New(1)

	if _, err := s.Complete(99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Complete(99) err=%v, want ErrNotFound", err)
	}

	mustArrive(t, s, nil, []string{"x"})
	mustArrive(t, s, nil, []string{"y"})

	if _, err := s.Complete(2); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Complete(2 waiting) err=%v, want ErrNotRunning", err)
	}
	requireStatus(t, s, 2, Waiting)
	t.Log("判定依据: 被拒绝的完成不得改变状态，T2 仍在等待")

	mustComplete(t, s, 1)
	if _, err := s.Complete(1); !errors.Is(err, ErrAlreadyCompleted) {
		t.Fatalf("Complete(1 completed) err=%v, want ErrAlreadyCompleted", err)
	}
}

// 取消校验：编号不存在、已完成、正在运行，按此顺序只报第一个。
func TestCancelErrorPrecedence(t *testing.T) {
	s := New(1)

	if _, err := s.Cancel(99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Cancel(99) err=%v, want ErrNotFound", err)
	}

	mustArrive(t, s, nil, []string{"x"})
	mustArrive(t, s, nil, []string{"y"})

	if _, err := s.Cancel(1); !errors.Is(err, ErrRunning) {
		t.Fatalf("Cancel(1 running) err=%v, want ErrRunning", err)
	}
	requireStatus(t, s, 1, Running)

	mustComplete(t, s, 1)
	if _, err := s.Cancel(1); !errors.Is(err, ErrAlreadyCompleted) {
		t.Fatalf("Cancel(1 completed) err=%v, want ErrAlreadyCompleted", err)
	}

	mustArrive(t, s, nil, []string{"z"})
	requireStatus(t, s, 3, Waiting)
	if _, err := s.Cancel(3); err != nil {
		t.Fatalf("Cancel(3 waiting) unexpected error: %v", err)
	}
	if _, err := s.Cancel(3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Cancel(3 cancelled) err=%v, want ErrNotFound", err)
	}
	t.Log("判定依据: 已取消的事务不再存在，不再阻挡他人")
}

type decl struct {
	reads  map[string]bool
	writes map[string]bool
}

func declConflict(a, b decl) bool {
	for k := range a.writes {
		if b.reads[k] || b.writes[k] {
			return true
		}
	}
	for k := range b.writes {
		if a.reads[k] || a.writes[k] {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// 随机事务与随机完成顺序下，真实执行结果与到达序串行执行逐键一致。
func TestRandomSerialEquivalence(t *testing.T) {
	for trial := 0; trial < 20; trial++ {
		trial := trial
		t.Run(fmt.Sprintf("trial=%02d", trial), func(t *testing.T) {
			runRandomTrial(t, rand.New(rand.NewSource(int64(1000+trial))))
		})
	}
}

func runRandomTrial(t *testing.T, rng *rand.Rand) {
	t.Helper()

	k := 1 + rng.Intn(4)
	n := 1 + rng.Intn(30)
	keyPool := []string{"a", "b", "c", "d", "e", "f"}

	randomSet := func() map[string]bool {
		set := map[string]bool{}
		for _, key := range keyPool {
			if rng.Intn(3) == 0 {
				set[key] = true
			}
		}
		return set
	}

	decls := make([]decl, n+1)
	for i := 1; i <= n; i++ {
		d := decl{reads: randomSet(), writes: randomSet()}
		for len(d.reads) == 0 && len(d.writes) == 0 {
			d = decl{reads: randomSet(), writes: randomSet()}
		}
		decls[i] = d
	}
	t.Logf("输入: K=%d, 事务数=%d", k, n)
	for i := 1; i <= n; i++ {
		t.Logf("输入: T%d reads=%v writes=%v", i, sortedKeys(decls[i].reads), sortedKeys(decls[i].writes))
	}

	s := New(k)
	store := map[string]int{}
	readsSeen := map[int]map[string]int{}
	running := map[int]bool{}
	unfinished := map[int]bool{}

	execute := func(id int) {
		snap := map[string]int{}
		for key := range decls[id].reads {
			snap[key] = store[key]
		}
		readsSeen[id] = snap
		for key := range decls[id].writes {
			store[key] = id
		}
		t.Logf("输出: 执行 T%d, 读到=%v, 写入=%v", id, snap, sortedKeys(decls[id].writes))
	}

	checkRelease := func(released []int) {
		for _, id := range released {
			if len(running) >= k {
				t.Fatalf("放行 T%d 前运行数已达 K=%d", id, k)
			}
			for other := range running {
				if declConflict(decls[id], decls[other]) {
					t.Fatalf("放行的 T%d 与运行中的 T%d 冲突", id, other)
				}
			}
			for earlier := range unfinished {
				if earlier < id && declConflict(decls[id], decls[earlier]) {
					t.Fatalf("放行的 T%d 与更早未完成的事务 T%d 冲突", id, earlier)
				}
			}
			running[id] = true
			execute(id)
		}
	}

	arrived := 0
	for arrived < n || len(running) > 0 {
		if len(running) > 0 && (arrived == n || rng.Intn(2) == 0) {
			ids := make([]int, 0, len(running))
			for id := range running {
				ids = append(ids, id)
			}
			sort.Ints(ids)
			victim := ids[rng.Intn(len(ids))]
			released, err := s.Complete(victim)
			if err != nil {
				t.Fatalf("Complete(%d) unexpected error: %v", victim, err)
			}
			delete(running, victim)
			delete(unfinished, victim)
			t.Logf("输出: Complete(%d) -> released=%v", victim, released)
			checkRelease(released)
		} else {
			arrived++
			d := decls[arrived]
			id, released, err := s.Arrive(sortedKeys(d.reads), sortedKeys(d.writes))
			if err != nil {
				t.Fatalf("Arrive unexpected error: %v", err)
			}
			if id != arrived {
				t.Fatalf("arrive id=%d, want %d (编号须按到达顺序连续)", id, arrived)
			}
			unfinished[id] = true
			t.Logf("输出: Arrive(T%d) -> released=%v", id, released)
			checkRelease(released)
		}
	}

	serialStore := map[string]int{}
	serialReads := map[int]map[string]int{}
	for i := 1; i <= n; i++ {
		snap := map[string]int{}
		for key := range decls[i].reads {
			snap[key] = serialStore[key]
		}
		serialReads[i] = snap
		for key := range decls[i].writes {
			serialStore[key] = i
		}
	}

	if !reflect.DeepEqual(store, serialStore) {
		t.Fatalf("最终存储不一致:\n真实=%v\n串行=%v", store, serialStore)
	}
	if !reflect.DeepEqual(readsSeen, serialReads) {
		t.Fatalf("读快照不一致:\n真实=%v\n串行=%v", readsSeen, serialReads)
	}
	t.Logf("判定依据: 逐键比较真实执行与到达序串行执行的最终存储与每次读快照，二者完全一致 (store=%v)", store)
}

// 相同操作序列重放结果完全相同。
func TestDeterministicReplay(t *testing.T) {
	type op struct {
		kind   string
		reads  []string
		writes []string
		id     int
	}
	script := []op{
		{kind: "arrive", writes: []string{"a"}},
		{kind: "arrive", reads: []string{"a"}},
		{kind: "arrive", writes: []string{"b"}},
		{kind: "arrive", reads: []string{"b"}},
		{kind: "complete", id: 3},
		{kind: "arrive", reads: []string{"a"}, writes: []string{"c"}},
		{kind: "cancel", id: 2},
		{kind: "complete", id: 1},
		{kind: "complete", id: 4},
		{kind: "complete", id: 5},
	}

	replay := func() [][]int {
		s := New(2)
		var outs [][]int
		for _, o := range script {
			var released []int
			var err error
			switch o.kind {
			case "arrive":
				_, released, err = s.Arrive(o.reads, o.writes)
			case "complete":
				released, err = s.Complete(o.id)
			case "cancel":
				released, err = s.Cancel(o.id)
			}
			if err != nil {
				t.Fatalf("op %+v unexpected error: %v", o, err)
			}
			outs = append(outs, released)
		}
		return outs
	}

	first := replay()
	second := replay()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放结果不一致:\n第一次=%v\n第二次=%v", first, second)
	}
	t.Logf("判定依据: 相同操作序列两次重放的放行序列完全一致: %v", first)
}

// 到达、完成、取消与查询可被并发调用（配合 -race 验证）。
func TestConcurrentAccess(t *testing.T) {
	const n = 200
	s := New(4)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", i%8)
			if _, _, err := s.Arrive(nil, []string{key}); err != nil {
				t.Errorf("Arrive unexpected error: %v", err)
			}
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for id := 1; id <= n; id++ {
			for {
				st, ok := s.Status(id)
				if ok && st == Running {
					if _, err := s.Complete(id); err == nil {
						break
					}
				}
			}
		}
	}()

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := 1; id <= n; id++ {
				_, _ = s.Blockers(id)
			}
		}()
	}

	wg.Wait()
	for id := 1; id <= n; id++ {
		requireStatus(t, s, id, Completed)
	}
}
