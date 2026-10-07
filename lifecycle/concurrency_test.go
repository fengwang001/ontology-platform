package lifecycle

import (
	"sort"
	"sync"
	"testing"
)

// 并发触发同一实例：所有请求必须等价于某个串行顺序。
// 这里用互不相同的属性做 CAS 风格校验：最终属性集合必须等于
// “所有被接受的 SetAttr 按某种串行顺序依次应用”的结果。
func TestConcurrentSameInstanceSerializable(t *testing.T) {
	st := seedStore(inst("o1", "order", "created", nil))
	eng := NewEngine(testSchema(), st, DiscardLogger{})

	const n = 80
	var wg sync.WaitGroup
	results := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "k" + itoa(i)
			res, _ := eng.Batch([]Op{SetAttr("o1", key, i)})
			results[i] = res.Committed
		}(i)
	}
	wg.Wait()

	final := st.GetInstance("o1")
	if final.State != "created" {
		t.Fatal("状态不应被改变")
	}
	// 串行等价性：每个被接受的写入都必须在最终属性中，且值一致；
	// 被拒绝的写入一个都不允许出现。
	accepted := 0
	for i := 0; i < n; i++ {
		key := "k" + itoa(i)
		v, ok := final.Attrs[key]
		if results[i] {
			accepted++
			if !ok || v != i {
				t.Fatalf("accepted write %d missing/corrupt: ok=%v v=%v", i, ok, v)
			}
		} else if ok {
			t.Fatalf("rejected write %d leaked into final state", i)
		}
	}
	// 每个写入针对不同 key，互不冲突，串行顺序中全部可成功。
	if accepted != n {
		t.Fatalf("期望 %d 个写入全部成功, got %d", n, accepted)
	}
	if final.Clock == 0 {
		t.Fatal("成功提交应推进时间戳")
	}
}

// 并发迁移同一实例：终态后所有迁移/改属性必须被拒绝，
// 且最终状态唯一确定，等价于“恰好一条 close 先执行”的某个串行序。
func TestConcurrentTransitionsSameInstance(t *testing.T) {
	st := seedStore(inst("o1", "order", "paid", nil))
	eng := NewEngine(testSchema(), st, DiscardLogger{})

	const n = 60
	var wg sync.WaitGroup
	commits := 0
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _ := eng.Batch([]Op{Fire("o1", "close")})
			mu.Lock()
			if res.Committed {
				commits++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if commits != 1 {
		t.Fatalf("必须恰好一条 close 串行生效, got %d", commits)
	}
	if got := st.GetInstance("o1").State; got != "closed" {
		t.Fatalf("最终状态=%s", got)
	}
}

// 并发触发彼此无关的不同实例：必须互不阻塞，全部成功，
// 结果等价于任意一个串行排列。
func TestConcurrentIndependentInstances(t *testing.T) {
	var seeds []*Instance
	const n = 100
	for i := 0; i < n; i++ {
		seeds = append(seeds,
			inst("o"+itoa(i), "order", "created",
				map[string]AttrValue{"paid": true}))
	}
	st := seedStore(seeds...)
	eng := NewEngine(testSchema(), st, DiscardLogger{})

	var wg sync.WaitGroup
	ok := make([]bool, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res, _ := eng.Batch([]Op{Fire("o"+itoa(i), "pay")})
			ok[i] = res.Committed
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < n; i++ {
		if !ok[i] {
			t.Fatalf("独立实例 %d 的迁移不应被阻塞或拒绝", i)
		}
		if got := st.GetInstance("o" + itoa(i)).State; got != "paid" {
			t.Fatalf("实例 %d 最终状态=%s", i, got)
		}
	}
	clocks := []int64{}
	for i := 0; i < n; i++ {
		clocks = append(clocks, st.GetInstance("o"+itoa(i)).Clock)
	}
	sort.Slice(clocks, func(a, b int) bool { return clocks[a] < clocks[b] })
	if clocks[0] <= 0 || clocks[n-1] > int64(n) {
		t.Fatalf("时钟戳应来自 1..%d 的某个串行序, got %d..%d",
			n, clocks[0], clocks[n-1])
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
