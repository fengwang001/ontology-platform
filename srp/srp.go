// Package srp 实现多单元资源的栈资源策略（Stack Resource Policy, SRP）启动闸门。
//
// 抢占层级 π 由当前全部任务的相对截止期 D 动态推出；资源天花板按余量
// avail 动态推出；Start 是唯一的启动闸门，通过后作业压栈，之后取放资源
// 只需栈顶判定。所有公开操作可并发调用，等价于某一串行顺序。
package srp

import "sync"

const (
	maxResources = 8
	maxTasks     = 16
	maxJobs      = 32
)

// JobInfo 是作业的外部只读视图。
type JobInfo struct {
	ID     string
	TaskID string
}

// SRP 是栈资源策略闸门。零值不可用，请使用 New 创建。
type SRP struct {
	mu        sync.Mutex
	resources map[string]*resource
	tasks     map[string]*task
	jobs      map[string]*job
	stack     []*job // 栈底在下、栈顶在上

	// shortageRejections 为非导出断言计数器：在合法操作序列下必须恒为 0。
	shortageRejections int
}

type resource struct {
	id    string
	n     int
	avail int
}

type task struct {
	id string
	d  int
	mu map[string]int
}

type job struct {
	id   string
	task *task
	held map[string]int
}

// New 创建一个空的 SRP 闸门。
func New() *SRP {
	return &SRP{
		resources: map[string]*resource{},
		tasks:     map[string]*task{},
		jobs:      map[string]*job{},
	}
}

// Mu 是 AddTask 中单个资源的最大需求声明。
type Mu struct {
	Resource string
	Units    int
}
