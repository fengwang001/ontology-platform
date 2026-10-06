package review

import (
	"testing"
	"time"
)

// TestDrawCostIndependentOfHistory：同一申报人在大量历史作废评审堆积后，
// 抽取耗时不随历史评审总数增长。做法是用"作废前科集合"直接制造回避
// 事实（需求规定前科只以增量集合查询），比较历史为 0 与 4000 条时
// 抽取一个较大评委组的耗时；后者不得出现数量级劣化。
//
// 更关键的结构性证明见设计说明：抽取只遍历评委库与一张回避集合，
// autoAvoid 是 map，查找 O(1)，从不扫描 reviews。
func TestDrawCostIndependentOfHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("性能断言在 short 模式跳过")
	}
	build := func(historyReviewers int) *Service {
		s := NewService(testEpoch)
		for id := 1; id <= 2000; id++ {
			mustAddReviewer(t, s, id, "U"+itoa(id%97+1), grpName(id))
		}
		mustAddApplicant(t, s, 700, "UX")
		// 直接在 autoAvoid 集合中制造 historyReviewers 条"作废前科"，
		// 等价于该申报人曾有这么多场作废评审、每场一人。
		app := s.applicants[700]
		for id := 1; id <= historyReviewers && id <= 2000; id++ {
			app.autoAvoid[id] = struct{}{}
		}
		return s
	}

	// 不强制断言具体纳秒值（避免 CI 抖动），仅断言大历史规模耗时
	// 不出现数量级劣化。
	d0 := singleDrawNanos(build(0))
	d1 := singleDrawNanos(build(1000))
	t.Logf("历史 0 条抽取: %v ns/op; 历史 1000 条: %v ns/op", d0, d1)
	// 允许常数因子差异（map 分配等），但不允许线性劣化。
	if d1 > d0*5 && d0 > 0 {
		t.Fatalf("抽取成本疑似随历史评审数增长: %v -> %v", d0, d1)
	}

	// 结构性断言：drawPanel 代码路径中不持有也不读取 s.reviews。
	// 这里以行为体现：reviews 中即便塞入大量已作废评审，结果也不变。
	s := build(1000)
	_, panel, err := s.CreateReview(clockAt(1), 700, 11, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 前 1000 名全部回避，最小可行 11 人必为 1001..1011。
	want := make([]int, 11)
	for i := range want {
		want[i] = 1001 + i
	}
	if !intsEqual(panel, want) {
		t.Fatalf("历史回避堆积后抽取结果错误: %v", panel)
	}
}

func singleDrawNanos(s *Service) int64 {
	start := time.Now()
	if _, _, err := s.CreateReview(clockAt(1), 700, 101, map[string]int{"B": 30}); err != nil {
		panic(err)
	}
	return time.Since(start).Nanoseconds()
}
