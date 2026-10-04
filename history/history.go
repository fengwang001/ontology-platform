// Package history 维护每个测试用例的时长样本、标记窗口与隔离状态。
// 历史只在 Finish 时被更新，更新次序即 Finish 的次序。
package history

// Mark 是用例在一次构建中的最终标记。
type Mark int

const (
	Clean  Mark = iota // 首次即通过
	Flaky              // 失败后重试通过
	Broken             // 用尽重试仍失败
)

// Record 是单个用例的历史记录。
type Record struct {
	Samples     []int64 // 最近 D 次通过时长（毫秒），最旧在前
	Window      []Mark  // 最近 W 个标记（仅未隔离时记入），最旧在前
	Quarantined bool    // 当前是否被隔离
	CleanStreak int     // 隔离期间连续 Clean 次数
}

// Store 按固定参数维护全部用例的历史记录。
type Store struct {
	w, f, p, d int
	records    map[string]*Record
}

// NewStore 创建历史存储：w 为标记窗口长度，f 为隔离阈值，
// p 为解除所需连续干净次数，d 为时长样本数。
func NewStore(w, f, p, d int) *Store {
	return &Store{w: w, f: f, p: p, d: d, records: make(map[string]*Record)}
}

// Est 返回用例的估计时长：最近样本之和除以样本数向上取整。
// 无样本时 ok 为 false。
func (s *Store) Est(test string) (est int64, ok bool) {
	r, ok := s.records[test]
	if !ok || len(r.Samples) == 0 {
		return 0, false
	}
	var sum int64
	for _, v := range r.Samples {
		sum += v
	}
	n := int64(len(r.Samples))
	return (sum + n - 1) / n, true
}

// Quarantined 报告用例此刻是否被隔离（无历史视为未隔离）。
func (s *Store) Quarantined(test string) bool {
	r, ok := s.records[test]
	return ok && r.Quarantined
}

// Update 在 Finish 时按此刻的实时隔离状态更新单个用例的历史。
// mark 为本次构建的最终标记；若通过（Clean/Flaky），ms 为通过那一次的时长。
func (s *Store) Update(test string, mark Mark, ms int64) {
	r := s.records[test]
	if r == nil {
		r = &Record{}
		s.records[test] = r
	}
	if mark != Broken {
		r.Samples = append(r.Samples, ms)
		if len(r.Samples) > s.d {
			r.Samples = append([]int64(nil), r.Samples[len(r.Samples)-s.d:]...)
		}
	}
	if !r.Quarantined {
		r.Window = append(r.Window, mark)
		if len(r.Window) > s.w {
			r.Window = append([]Mark(nil), r.Window[len(r.Window)-s.w:]...)
		}
		flaky := 0
		for _, m := range r.Window {
			if m == Flaky {
				flaky++
			}
		}
		if flaky >= s.f {
			r.Quarantined = true
			r.CleanStreak = 0
		}
		return
	}
	// 已隔离：标记不入窗口。
	if mark == Clean {
		r.CleanStreak++
		if r.CleanStreak >= s.p {
			r.Quarantined = false
			r.CleanStreak = 0
			r.Window = nil
		}
	} else {
		r.CleanStreak = 0
	}
}
