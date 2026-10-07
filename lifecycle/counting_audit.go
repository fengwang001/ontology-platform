package lifecycle

import "sync"

// CountingAuditLog 包装一个底层审计日志并统计被读取的历史记录条数。
// 可见性判定路径若读取审计记录就会在这里留下可复现的计数；
// 生产实现从不读取，因而读数恒为 0。
type CountingAuditLog struct {
	inner AuditLog

	mu       sync.Mutex
	reads    int
	appends  int
	readSnap int
}

func NewCountingAuditLog(inner AuditLog) *CountingAuditLog {
	return &CountingAuditLog{inner: inner}
}

func (l *CountingAuditLog) Append(t Transition) {
	l.mu.Lock()
	l.appends++
	l.mu.Unlock()
	if l.inner != nil {
		l.inner.Append(t)
	}
}

// TouchRecord 模拟/统计“读到一条历史转换记录”。
func (l *CountingAuditLog) TouchRecord() {
	l.mu.Lock()
	l.reads++
	l.mu.Unlock()
}

// TouchN 统计一次判定读到 n 条历史记录。
func (l *CountingAuditLog) TouchN(n int) {
	l.mu.Lock()
	l.reads += n
	l.mu.Unlock()
}

func (l *CountingAuditLog) Reads() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reads
}

func (l *CountingAuditLog) Appends() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.appends
}

// BeginCheckpoint 开始一段“仅统计可见性判定读取量”的观测窗口。
func (l *CountingAuditLog) BeginCheckpoint() {
	l.mu.Lock()
	l.readSnap = l.reads
	l.mu.Unlock()
}

// ReadsSinceCheckpoint 返回观测窗口内新增的历史读取条数。
func (l *CountingAuditLog) ReadsSinceCheckpoint() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reads - l.readSnap
}
