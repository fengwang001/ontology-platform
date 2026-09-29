package di

import "sync"

// Container 是带生命周期的依赖注入容器。
type Container struct {
	mu       sync.Mutex
	regs     map[string]*Registration
	regOrder []string
	frozen   bool
	closed   bool

	root *Scope
}

// Scope 表示一个解析作用域。
type Scope struct {
	container *Container
	name      string
	isRoot    bool

	mu sync.Mutex

	// 缓存：singleton 只在 root 上缓存；scoped 在各自作用域缓存。
	cached map[string]any
	// 正在构造中的单例/作用域实例（同批等待者共享同一构造结果）。
	building map[string]*buildState

	// 本作用域拥有的实例（按构造成功顺序），关闭时逆序释放。
	owned []*node

	closed      bool
	active      sync.WaitGroup
	closeMu     sync.Mutex
	childScopes []*Scope
}

// New 创建一个空容器。
func New() *Container {
	c := &Container{
		regs: make(map[string]*Registration),
	}
	c.root = &Scope{
		container: c,
		name:      "root",
		isRoot:    true,
		cached:    make(map[string]any),
		building:  make(map[string]*buildState),
	}
	return c
}
