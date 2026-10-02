package batchisolate

import (
	"fmt"
	"sync"
	"testing"
)

// 两个并发 Submit 互不可见彼此新入表者：
// Submit A 判定 a 为 Poison 并入表；Submit B 起始快照不含 a，
// 因此 B 自己调用 sink 处理 a（这里让其成功交付）。
func TestConcurrentSubmitSnapshotIsolation(t *testing.T) {
	// 两个 Submit 的“首次 sink 调用”在屏障处汇合：
	//   - 快照在任何 sink 调用前读取，故汇合时双方快照均已固定；
	//   - 放行前没有任何调用返回，因此 A 不可能已判定/并入毒丸。
	// 放行后按批内容分流：A 的批 ["a","x"] 永久失败，B 的批 ["a","b"] 成功；
	// 随后 A 对 [a]、[x] 的调用不再屏障。
	firstArrived := make(chan struct{}, 2)
	barrier := make(chan struct{})
	var sinkMu sync.Mutex

	iso := mustNew(t, 0, 100, 100, func(ids []string) error {
		sinkMu.Lock()
		key := setKey(ids)
		isFirst := key == fmt.Sprintf("%q", []string{"a", "x"}) ||
			key == fmt.Sprintf("%q", []string{"a", "b"})
		sinkMu.Unlock()
		if isFirst {
			firstArrived <- struct{}{}
			<-barrier
		}
		if key == fmt.Sprintf("%q", []string{"a", "x"}) ||
			key == fmt.Sprintf("%q", []string{"a"}) {
			return errPermanent
		}
		return nil
	})

	var wg sync.WaitGroup
	var resA, resB *Result
	wg.Add(2)
	go func() { defer wg.Done(); resA, _ = iso.Submit([]string{"a", "x"}) }()
	<-firstArrived // A 已在首次调用处等待
	go func() { defer wg.Done(); resB, _ = iso.Submit([]string{"a", "b"}) }()
	<-firstArrived // B 已在首次调用处等待；此时 A 尚未返回、更未并入
	close(barrier)
	wg.Wait()

	if fmt.Sprint(resA.Dead) != "[{a Poison}]" || fmt.Sprint(resA.Delivered) != "[x]" {
		t.Fatalf("A: dead=%v delivered=%v", resA.Dead, resA.Delivered)
	}
	if fmt.Sprint(resB.Delivered) != "[a b]" || len(resB.Dead) != 0 || resB.Calls != 1 {
		t.Fatalf("B: delivered=%v dead=%v calls=%d（B 不应看见 A 并入的 a）",
			resB.Delivered, resB.Dead, resB.Calls)
	}
	if fmt.Sprint(iso.Known()) != "[a]" {
		t.Fatalf("known=%v want [a]", iso.Known())
	}
	t.Logf("并发快照隔离: A=%v/%v B=%v/%v B.Calls=%d 最终表=%v",
		resA.Delivered, resA.Dead, resB.Delivered, resB.Dead, resB.Calls, iso.Known())
}

// 大量并发 Submit 下的基本不变量：不重不漏、计数一致、表不超容量、无重复。
func TestConcurrentInvariants(t *testing.T) {
	const workers = 16
	const perWorker = 20

	sink := newScriptSink(nil, nil)
	iso := mustNew(t, 1, 50, 100000, sink.Write)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				ids := []string{
					fmt.Sprintf("w%d-j%d-0", w, j),
					fmt.Sprintf("w%d-j%d-1", w, j),
				}
				res, err := iso.Submit(ids)
				if err != nil {
					t.Errorf("submit: %v", err)
					return
				}
				if len(res.Delivered)+len(res.Dead) != len(ids) {
					t.Errorf("union mismatch")
					return
				}
			}
		}(w)
	}
	wg.Wait()

	if got := iso.Known(); len(got) > 50 {
		t.Fatalf("table size %d > Km", len(got))
	}
	seen := map[string]struct{}{}
	for _, id := range iso.Known() {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate in table: %s", id)
		}
		seen[id] = struct{}{}
	}
}

// 预置已知表 + 多 Submit 的随机对照：第二个 Submit 起始表为第一个 Submit 结束表。
func TestTwoSubmitChainedRandom(t *testing.T) {
	// 固定构造：初始表含 i01；第一批 [i00..i05]，i04 毒丸；
	// 第二批提交含 i04 与 i01（应摘出 Known）及新编号。
	poison := map[string]bool{"i04": true}
	trans := map[string]int{}

	sim1 := newSimSink(poison, trans)
	simRes1 := naiveSim(idsRange("i", 6), 1, 3, 100, sim1, []string{"i01"})

	batch2 := []string{"i04", "i01", "j0", "j1", "i02"}
	sim2 := newSimSink(poison, trans)
	simRes2 := naiveSim(batch2, 1, 3, 100, sim2, simRes1.known)

	// 真实链：可切换 sink 的包装，使两个 Submit 各自对照独立的模拟脚本计数。
	var active func([]string) error
	iso2, _ := New(1, 3, 100, func(ids []string) error { return active(ids) })
	iso2.mergePoison([]string{"i01"})
	a1 := newSimSink(poison, trans)
	active = a1.write
	got1, err := iso2.Submit(idsRange("i", 6))
	if err != nil {
		t.Fatal(err)
	}
	a2 := newSimSink(poison, trans)
	active = a2.write
	got2, err := iso2.Submit(batch2)
	if err != nil {
		t.Fatal(err)
	}

	if fmt.Sprint(got1.Delivered) != fmt.Sprint(simRes1.delivered) ||
		fmt.Sprint(got1.Dead) != fmt.Sprint(simRes1.dead) ||
		got1.Calls != simRes1.calls {
		t.Fatalf("submit1 mismatch:\n got=%v %v %d\nwant=%v %v %d",
			got1.Delivered, got1.Dead, got1.Calls,
			simRes1.delivered, simRes1.dead, simRes1.calls)
	}
	if fmt.Sprint(got2.Delivered) != fmt.Sprint(simRes2.delivered) ||
		fmt.Sprint(got2.Dead) != fmt.Sprint(simRes2.dead) ||
		got2.Calls != simRes2.calls {
		t.Fatalf("submit2 mismatch:\n got=%v %v %d\nwant=%v %v %d",
			got2.Delivered, got2.Dead, got2.Calls,
			simRes2.delivered, simRes2.dead, simRes2.calls)
	}
	if fmt.Sprint(iso2.Known()) != fmt.Sprint(simRes2.known) {
		t.Fatalf("final table got=%v want=%v", iso2.Known(), simRes2.known)
	}
	t.Logf("链式两 Submit:\n S1 交付=%v 死信=%v Calls=%d 表=%v\n S2 输入=%v 交付=%v 死信=%v Calls=%d 表=%v",
		got1.Delivered, got1.Dead, got1.Calls, simRes1.known,
		batch2, got2.Delivered, got2.Dead, got2.Calls, simRes2.known)
}
