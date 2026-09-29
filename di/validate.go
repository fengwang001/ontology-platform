package di

import (
	"fmt"
	"sort"
)

// Register 添加一条注册项；冻结或关闭后返回错误。
// 重复名称在 Freeze 时按注册顺序报告为第一个错误。
func (c *Container) Register(r Registration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrContainerClosed
	}
	if c.frozen {
		return ErrContainerFrozen
	}
	if r.Construct == nil {
		return &RegistrationError{Kind: "invalid registration", Detail: fmt.Sprintf("service %q has nil constructor", r.Name)}
	}
	c.regs[r.Name] = &r
	c.regOrder = append(c.regOrder, r.Name)
	return nil
}

// Freeze 校验全部注册项并冻结容器。
// 检查顺序固定：重复注册 -> 依赖未注册 -> 依赖成环 -> 生命周期俘获；
// 只返回第一个错误，且失败时不冻结，容器可以继续注册。
func (c *Container) Freeze() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrContainerClosed
	}
	if c.frozen {
		return ErrContainerFrozen
	}

	regs := make(map[string]*Registration, len(c.regOrder))
	var order []string

	// 1) 重复注册：按注册顺序发现第一个重名项。
	seen := make(map[string]int, len(c.regOrder))
	for idx, name := range c.regOrder {
		if first, ok := seen[name]; ok {
			return &RegistrationError{
				Kind:   "duplicate registration",
				Detail: fmt.Sprintf("service %q registered more than once (first at index %d, duplicated at index %d)", name, first, idx),
			}
		}
		seen[name] = idx
		r := c.regs[name]
		regs[name] = r
		order = append(order, name)
	}

	// 2) 依赖未注册：按注册顺序检查每条注册项的依赖。
	for _, name := range order {
		r := regs[name]
		for _, dep := range r.Dependencies {
			if _, ok := regs[dep]; !ok {
				return &RegistrationError{
					Kind:   "missing dependency",
					Detail: fmt.Sprintf("service %q depends on unregistered service %q", name, dep),
				}
			}
		}
	}

	// 3) 依赖成环：从每个服务做 DFS，按注册顺序返回第一个环。
	color := make(map[string]int, len(order)) // 0=未访问 1=在栈上 2=完成
	var stack []string
	var cycleAt string
	var dfs func(name string) bool
	dfs = func(name string) bool {
		color[name] = 1
		stack = append(stack, name)
		for _, dep := range regs[name].Dependencies {
			switch color[dep] {
			case 0:
				if dfs(dep) {
					return true
				}
			case 1:
				cycleAt = dep
				return true
			}
		}
		color[name] = 2
		stack = stack[:len(stack)-1]
		return false
	}
	for _, name := range order {
		if color[name] == 0 {
			stack = stack[:0]
			if dfs(name) {
				path := append([]string(nil), stack...)
				path = append(path, cycleAt)
				return &RegistrationError{
					Kind:   "circular dependency",
					Detail: fmt.Sprintf("dependency cycle: %v", path),
				}
			}
		}
	}

	// 4) 生命周期俘获：
	// 单例的依赖闭包中，穿过瞬态继续展开、遇单例停止，不得出现作用域服务。
	singletons := make([]string, 0)
	for _, name := range order {
		if regs[name].Lifetime == Singleton {
			singletons = append(singletons, name)
		}
	}
	sort.Strings(singletons)
	type frame struct {
		from string
		dep  string
	}
	for _, start := range singletons {
		visited := map[string]bool{start: true}
		var stack []frame
		for _, d := range regs[start].Dependencies {
			stack = append(stack, frame{from: start, dep: d})
		}
		for len(stack) > 0 {
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			depReg := regs[f.dep]
			switch depReg.Lifetime {
			case Scoped:
				return &RegistrationError{
					Kind: "lifetime capture",
					Detail: fmt.Sprintf(
						"singleton %q transitively captures scoped service %q via %q",
						start, f.dep, f.from),
				}
			case Singleton:
				// 遇单例停止展开：它有自己独立的、已保证无俘获的闭包。
				continue
			case Transient:
				if visited[f.dep] {
					continue
				}
				visited[f.dep] = true
				for _, d := range depReg.Dependencies {
					stack = append(stack, frame{from: f.dep, dep: d})
				}
			}
		}
	}

	c.frozen = true
	return nil
}

// NewScope 创建一个新的子作用域。
func (c *Container) NewScope(name string) (*Scope, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrContainerClosed
	}
	if !c.frozen {
		return nil, ErrNotFrozen
	}
	s := &Scope{
		container: c,
		name:      name,
		cached:    make(map[string]any),
		building:  make(map[string]*buildState),
	}
	c.root.childScopes = append(c.root.childScopes, s)
	return s, nil
}

// Closed 报告容器是否已关闭。
func (c *Container) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
