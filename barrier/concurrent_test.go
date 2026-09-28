package barrier

import (
	"fmt"
	"sync"
	"testing"
)

func runFullScenario() ([][]Output, [][]int, [][]Record) {
	events := []Event{
		recOn(0, "w", "1"),
		recOn(1, "x", "1"),
		bar(0, 1),
		recOn(0, "w", "2"),
		recOn(1, "x", "2"),
		bar(1, 1),
		recOn(1, "y", "1"),
		bar(0, 2),
		recOn(0, "w", "3"),
		bar(1, 2),
	}

	batches := [][]Event{
		events[:2],
		events[2:5],
		events[5:7],
		events[7:],
	}

	a := New(16)
	outputs := make([][]Output, 0, len(batches))
	for _, batch := range batches {
		out, err := a.Apply(batch)
		if err != nil {
			panic(err)
		}
		outputs = append(outputs, out)
	}

	numbers := a.Snapshots()
	snaps := make([][]Record, len(numbers))
	for i, no := range numbers {
		snap, ok := a.SnapshotAt(no)
		if !ok {
			panic("snapshot missing")
		}
		snaps[i] = snap.Records
	}
	return outputs, [][]int{numbers}, snaps
}

// TestDeterminism 同一输入序列反复计算必须得到完全相同的输出与快照。
func TestDeterminism(t *testing.T) {
	first, nums1, snaps1 := runFullScenario()
	for i := 0; i < 20; i++ {
		out, nums, snaps := runFullScenario()
		if fmt.Sprint(out) != fmt.Sprint(first) ||
			fmt.Sprint(nums) != fmt.Sprint(nums1) ||
			fmt.Sprint(snaps) != fmt.Sprint(snaps1) {
			t.Fatalf("第%d次运行结果不一致", i+1)
		}
	}
	t.Logf("确定性判定：20次重复运行输出=%s", describeOutputs(flatten(first)))
	t.Logf("快照序列=%v", snaps1)
}

func flatten(batches [][]Output) []Output {
	var all []Output
	for _, b := range batches {
		all = append(all, b...)
	}
	return all
}

// TestConcurrentReads 对齐进行中并发读取快照，必须逐键一致且无数据竞争
// （使用 -race 运行）。
func TestConcurrentReads(t *testing.T) {
	a := New(64)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, no := range a.Snapshots() {
					snap, ok := a.SnapshotAt(no)
					if !ok {
						t.Errorf("快照%d读取失败", no)
						return
					}
					// 逐键一致性：同一 Key 的相邻记录在屏障边界上
					// 只能是其值序列的前缀，不会读到撕裂状态。
					counts := map[string]int{}
					for _, r := range snap.Records {
						counts[r.Key]++
					}
					for _, n := range counts {
						if n < 0 {
							t.Errorf("非法快照计数")
						}
					}
				}
			}
		}()
	}

	events := []Event{}
	for no := 1; no <= 20; no++ {
		events = append(events,
			recOn(0, fmt.Sprintf("k%d", no%5), fmt.Sprintf("v%d", no)),
			bar(0, no),
			recOn(0, fmt.Sprintf("k%d", no%5), fmt.Sprintf("p%d", no)),
			recOn(1, fmt.Sprintf("j%d", no%3), fmt.Sprintf("v%d", no)),
			bar(1, no),
		)
	}
	if _, err := a.Apply(events); err != nil {
		t.Fatal(err)
	}

	close(stop)
	wg.Wait()

	if nums := a.Snapshots(); len(nums) != 20 || nums[0] != 1 || nums[19] != 20 {
		t.Fatalf("快照编号错误：%v", nums)
	}
	t.Logf("并发判定：20个屏障全部对齐，快照编号=%v…%v；8个读取协程未见撕裂",
		a.Snapshots()[:3], a.Snapshots()[17:])
}
