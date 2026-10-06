package gc

import (
	"sort"
	"sync"
)

// Controller 是级联删除控制器。所有公开方法都可并发调用，
// 其结果等价于某个串行顺序；每次删除引发的级联在锁内收敛，
// 对外表现为不可分割。
type Controller struct {
	mu sync.Mutex

	objects map[string]*object
	// dependents 是反向索引：ownerID -> 依赖者 ID 集合，
	// 使级联只触及实际受影响的对象。
	dependents map[string]map[string]struct{}

	// propQ / remQ 是 settle 的工作队列，仅在持锁的 settle 内使用。
	propQ []*object
	remQ  []*object

	// steps 统计上一次公开操作触及的对象/引用次数，
	// 用于以可验证的方式证明开销与无关对象总数无关。
	steps int
}

// New 创建一个空控制器。
func New() *Controller {
	return &Controller{
		objects:    make(map[string]*object),
		dependents: make(map[string]map[string]struct{}),
	}
}

// LastOpSteps 返回上一次公开操作在级联与校验中触及的对象/引用次数。
// 该值只取决于被实际影响的对象与引用数量，与无关对象总数无关。
func (c *Controller) LastOpSteps() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.steps
}

// Get 返回对象的只读快照；不存在时 ok=false。
func (c *Controller) Get(id string) (View, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.objects[id]
	if !ok {
		return View{}, false
	}
	return viewOf(o), true
}

// Dump 返回全部对象的只读快照，按 ID 升序，供测试逐字段对照。
func (c *Controller) Dump() []View {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]View, 0, len(c.objects))
	for _, o := range c.objects {
		out = append(out, viewOf(o))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func viewOf(o *object) View {
	v := View{
		ID:         o.id,
		Owners:     make([]OwnerRef, 0, len(o.owners)),
		Finalizers: append([]string(nil), o.finalizers...),
		Deleting:   o.deleting,
		Policy:     o.policy,
		ReqTime:    o.reqTime,
	}
	for id, block := range o.owners {
		v.Owners = append(v.Owners, OwnerRef{OwnerID: id, Block: block})
	}
	sort.Slice(v.Owners, func(i, j int) bool { return v.Owners[i].OwnerID < v.Owners[j].OwnerID })
	return v
}

// addDependent 维护反向索引。
func (c *Controller) addDependent(ownerID, depID string) {
	s, ok := c.dependents[ownerID]
	if !ok {
		s = make(map[string]struct{})
		c.dependents[ownerID] = s
	}
	s[depID] = struct{}{}
}

func (c *Controller) removeDependent(ownerID, depID string) {
	if s, ok := c.dependents[ownerID]; ok {
		delete(s, depID)
		if len(s) == 0 {
			delete(c.dependents, ownerID)
		}
	}
}
