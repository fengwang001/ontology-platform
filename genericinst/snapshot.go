package genericinst

import (
	"io"
	"sort"
	"sync"
	"time"
)

// Snapshot 是登记中心某一瞬间的只读汇总视图。
// 所有字段在同一临界区内采样，来自同一瞬间。
type Snapshot struct {
	TakenAt      time.Time
	ValidCount   int            // 当前有效（未过期）实例数
	StaleCount   int            // 过期实例数（清理前仍占用配额）
	PerDefValid  map[string]int // 每个定义的有效实例数（仅含有实例的定义）
	PerDefStale  map[string]int // 每个定义的过期实例数（仅含有过期实例的定义）
	TotalHits    int            // 累计命中次数
	TotalCreates int            // 累计净新建次数（回滚不计）
}

// SnapshotView 返回同一瞬间的只读汇总视图。
func (r *Registry) SnapshotView() *Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := &Snapshot{
		TakenAt:      time.Now(),
		PerDefValid:  map[string]int{},
		PerDefStale:  map[string]int{},
		TotalHits:    r.totalHits,
		TotalCreates: r.totalCreates,
	}
	for _, in := range r.instances {
		if in.stale != nil {
			s.StaleCount++
			s.PerDefStale[in.Def]++
		} else {
			s.ValidCount++
			s.PerDefValid[in.Def]++
		}
	}
	return s
}

// Logger 记录每条输入、实际输出与判定依据。
type Logger interface {
	Log(event string, kv ...string)
}

type nopLogger struct{}

func (nopLogger) Log(string, ...string) {}

// NewTextLogger 把结构化日志写入给定 Writer（每条一行，带时间与事件）。
func NewTextLogger(w io.Writer) Logger {
	return &textLogger{mu: &sync.Mutex{}, w: w}
}

type textLogger struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *textLogger) Log(event string, kv ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := time.Now().Format("15:04:05.000") + " " + event
	for i := 0; i+1 < len(kv); i += 2 {
		line += " " + kv[i] + "=" + kv[i+1]
	}
	_, _ = io.WriteString(l.w, line+"\n")
}

// TestLogger 在内存中保留日志，供测试断言与打印。
type TestLogger struct {
	mu    sync.Mutex
	Lines []string
}

func NewTestLogger() *TestLogger { return &TestLogger{} }

func (l *TestLogger) Log(event string, kv ...string) {
	line := event
	for i := 0; i+1 < len(kv); i += 2 {
		line += " " + kv[i] + "=" + kv[i+1]
	}
	l.mu.Lock()
	l.Lines = append(l.Lines, line)
	l.mu.Unlock()
}

func sortedKeys(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
