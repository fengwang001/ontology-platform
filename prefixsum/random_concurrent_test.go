package prefixsum

import (
	"sync"
	"testing"
)

func TestRandomizedAgainstNaive(t *testing.T) {
	const (
		minKey = int64(-50)
		maxKey = int64(50)
		maxCap = 40
	)
	v, _ := New(Config{MinKey: minKey, MaxKey: maxKey, MaxKeys: maxCap})
	m := newNaive(minKey, maxKey)

	var state uint64 = 0x1234567
	next := func() uint64 {
		state = state*6364136223846793005 + 1442695040888963407
		return state
	}

	for step := 1; step <= 4000; step++ {
		key := minKey - 2 + int64(next()%uint64(maxKey-minKey+5)) // 偶尔越界
		switch next() % 10 {
		case 0, 1, 2, 3, 4: // put
			val := int64(next()%21) - 10
			got, gerr := v.Put(key, val)
			switch {
			case key < minKey || key > maxKey:
				if ErrorReason(gerr) != ReasonKeyOutOfRange {
					t.Fatalf("step %d: 期望越界 got=%v", step, gerr)
				}
			case len(m.data) >= maxCap && m.data[key] == 0 && !keyExists(m, key):
				if ErrorReason(gerr) != ReasonTooManyKeys {
					t.Fatalf("step %d: 期望超限 got=%v", step, gerr)
				}
			default:
				if gerr != nil {
					t.Fatalf("step %d put(%d,%d): 意外错误 %v", step, key, val, gerr)
				}
				want := m.put(key, val)
				if got != want {
					t.Fatalf("step %d put(%d,%d): affected got=%d want=%d", step, key, val, got, want)
				}
			}
		case 5, 6, 7: // delete
			got, gerr := v.Delete(key)
			switch {
			case key < minKey || key > maxKey:
				if ErrorReason(gerr) != ReasonKeyOutOfRange {
					t.Fatalf("step %d: 期望越界 got=%v", step, gerr)
				}
			case !keyExists(m, key):
				if ErrorReason(gerr) != ReasonKeyNotFound {
					t.Fatalf("step %d: 期望不存在 got=%v", step, gerr)
				}
			default:
				if gerr != nil {
					t.Fatalf("step %d delete(%d): 意外错误 %v", step, key, gerr)
				}
				want := m.del(key)
				if got != want {
					t.Fatalf("step %d delete(%d): affected got=%d want=%d", step, key, got, want)
				}
			}
		default: // prefix query
			sum, gerr := v.PrefixSum(key)
			if key < minKey || key > maxKey {
				if ErrorReason(gerr) != ReasonKeyOutOfRange {
					t.Fatalf("step %d: 期望越界 got=%v", step, gerr)
				}
			} else if !keyExists(m, key) {
				if ErrorReason(gerr) != ReasonKeyNotFound {
					t.Fatalf("step %d: 期望不存在 got=%v", step, gerr)
				}
			} else {
				if gerr != nil {
					t.Fatalf("step %d: 意外错误 %v", step, gerr)
				}
				entries, ok := m.recompute()
				if !ok {
					t.Fatalf("step %d: 朴素模型溢出", step)
				}
				var want int64
				for _, e := range entries {
					if e.Key == key {
						want = e.PrefixSum
					}
				}
				if sum != want {
					t.Fatalf("step %d prefix(%d): got=%d want=%d", step, key, sum, want)
				}
			}
		}

		// 小值域求和不会溢出；每步都与朴素重算全量比对。
		entries, ok := m.recompute()
		if !ok {
			t.Fatalf("step %d: 朴素模型溢出", step)
		}
		got := v.Snapshot()
		if len(got) != len(entries) {
			t.Fatalf("step %d: 快照长度 %d != %d", step, len(got), len(entries))
		}
		for i := range entries {
			if got[i] != entries[i] {
				t.Fatalf("step %d: 项%d got=%+v want=%+v", step, i, got[i], entries[i])
			}
		}
		if err := v.SelfCheck(); err != nil {
			t.Fatalf("step %d: 自检失败 %v", step, err)
		}
	}
	t.Logf("随机化 4000 步完成 判定依据=每步快照、单点查询、受影响键数、SelfCheck 均与朴素模型一致")
}

func keyExists(m *naiveModel, key int64) bool {
	_, ok := m.data[key]
	return ok
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	v, _ := New(Config{MinKey: 0, MaxKey: 200, MaxKeys: 300})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 多个执行体并发写入/删除（值域正负对称，键域足够大以避免溢出与频繁越界）。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			state := uint64(id)*2654435761 + 1
			for {
				select {
				case <-stop:
					return
				default:
				}
				state = state*6364136223846793005 + 1
				key := int64(state % 200)
				val := int64((state>>20)%9) - 4
				if state&1 == 0 {
					v.Put(key, val)
				} else {
					v.Delete(key)
				}
			}
		}(w)
	}

	// 多个执行体并发查询、快照与自检，可与写入删除并发。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			state := uint64(id)*40503 + 7
			for {
				select {
				case <-stop:
					return
				default:
				}
				state = state*6364136223846793005 + 1
				key := int64(state % 200)
				if _, err := v.PrefixSum(key); err != nil {
					if ErrorReason(err) != ReasonKeyNotFound {
						t.Errorf("并发查询返回非预期错误: %v", err)
						return
					}
				}
				snap := v.Snapshot()
				var prev int64 = -1
				for i, e := range snap {
					if i > 0 && e.Key <= prev {
						t.Errorf("并发快照顺序异常")
						return
					}
					prev = e.Key
				}
				if err := v.SelfCheck(); err != nil {
					t.Errorf("并发自检失败: %v", err)
					return
				}
			}
		}(r)
	}

	// 运行一段时间后停止；-race 由 go test -race 保证。
	// 用一个短忙等循环而不依赖 time.Sleep 的精度。
	deadline := make(chan struct{})
	go func() {
		var x int
		for i := 0; i < 2_000_000; i++ {
			x += i
		}
		_ = x
		close(deadline)
	}()
	<-deadline
	close(stop)
	wg.Wait()

	if err := v.SelfCheck(); err != nil {
		t.Fatalf("并发结束后自检失败: %v", err)
	}
	t.Logf("并发压测完成 Len=%d 判定依据=运行中多次 SelfCheck 通过且最终与朴素重算一致", v.Len())
}
