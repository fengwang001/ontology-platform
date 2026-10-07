package ontology

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// stateKey 将状态序列化为可比较的键。
func stateKey(st State) string {
	keys := make([]string, 0, len(st.Props))
	for k := range st.Props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := st.TypeID + "|"
	for _, k := range keys {
		out += fmt.Sprintf("%s=%v;", k, st.Props[k])
	}
	return out
}

// TestConcurrentAppendRebuildLinearizable 并发追加与并发重建必须等价于
// 某个全局串行执行：每个重建结果都必须等于最终事件流的某个前缀的朴素重放。
func TestConcurrentAppendRebuildLinearizable(t *testing.T) {
	rules := chainRules()
	s := NewStore(rules)
	const id = "hot"
	if err := s.Append(id, Created(1, "Root")); err != nil {
		t.Fatal(err)
	}

	var clock atomic.Int64
	clock.Store(1)

	const appenders = 8
	const opsPerAppender = 100
	const rebuilders = 4

	var observedMu sync.Mutex
	observed := map[string]State{}

	// 追加方：时间戳由共享原子时钟分配；若因并发交错导致时刻不再
	// 严格递增（ErrAmbiguousOrder）或取值越界等，换新时刻重试，
	// 体现“拒绝即无可观察改动”。
	var appendWg sync.WaitGroup
	for g := 0; g < appenders; g++ {
		appendWg.Add(1)
		go func(g int) {
			defer appendWg.Done()
			done := 0
			for done < opsPerAppender {
				tm := clock.Add(1)
				var err error
				switch (g + done) % 3 {
				case 0:
					err = s.Append(id, Set(tm, "score", Int(tm%101)))
				case 1:
					err = s.Append(id, Evolve(tm, []string{"Root", "Mid", "Leaf"}[done%3]))
				default:
					err = s.Append(id, Set(tm, "flag", Text("x")))
				}
				if err == nil {
					done++
					continue
				}
				if errors.Is(err, ErrAmbiguousOrder) {
					continue // 时刻冲突：换新时刻重试同一逻辑操作
				}
				// 越界/路径矛盾等拒绝属于预期结果，计入完成，避免定点重试死循环。
				if errors.Is(err, ErrOutOfRange) || errors.Is(err, ErrInvalidEvolutionPath) ||
					errors.Is(err, ErrUndefinedTargetType) {
					done++
					continue
				}
				{
					t.Errorf("意外错误: %v", err)
					return
				}
			}
		}(g)
	}
	// 重建方：持续读取“当前”状态并记录。
	stop := make(chan struct{})
	var rebuildWg sync.WaitGroup
	for r := 0; r < rebuilders; r++ {
		rebuildWg.Add(1)
		go func() {
			defer rebuildWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				st, _, err := s.Rebuild(id, math.MaxInt64)
				if err != nil {
					t.Errorf("重建失败: %v", err)
					return
				}
				observedMu.Lock()
				if len(observed) < 4096 { // 采样上限，去重记录
					observed[stateKey(st)] = st
				}
				observedMu.Unlock()
			}
		}()
	}
	appendWg.Wait()
	close(stop)
	rebuildWg.Wait()

	events, err := s.Events(id)
	if err != nil {
		t.Fatal(err)
	}
	// 预计算最终事件流全部前缀的朴素重放结果（键集合）。
	prefixKeys := make(map[string]bool, len(events))
	for k := 1; k <= len(events); k++ {
		st, err := NaiveReplay(events[:k], math.MaxInt64, rules)
		if err != nil {
			t.Fatalf("最终事件流前缀 %d 无法重放: %v", k, err)
		}
		prefixKeys[stateKey(st)] = true
	}
	observedMu.Lock()
	defer observedMu.Unlock()
	if len(observed) == 0 {
		t.Fatal("未观察到任何重建结果")
	}
	for key, st := range observed {
		if !prefixKeys[key] {
			t.Fatalf("观察到的状态不等于任何串行前缀: %+v", st)
		}
	}
	t.Logf("事件总数 %d, 观察到的去重状态 %d 个, 全部等价于某串行前缀", len(events), len(observed))
}

// TestConcurrentRebuildDeterministic 同一截止时刻的并发重建结果一致。
func TestConcurrentRebuildDeterministic(t *testing.T) {
	rules := chainRules()
	s := NewStore(rules)
	const id = "det"
	tm := int64(0)
	mustAppend(s, id, Created(1, "Mid"))
	for i := 0; i < 200; i++ {
		tm += 2
		mustAppend(s, id, Set(tm, "score", Int(int64(i%10))))
		tm += 2
		mustAppend(s, id, Evolve(tm, []string{"Root", "Mid", "Leaf"}[i%3]))
	}
	want := mustState(t, s, id, math.MaxInt64)
	wantKey := stateKey(want)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				got, _, err := s.Rebuild(id, math.MaxInt64)
				if err != nil {
					t.Error(err)
					return
				}
				if stateKey(got) != wantKey {
					t.Errorf("并发重建结果不一致:\n got %+v\nwant %+v", got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}
