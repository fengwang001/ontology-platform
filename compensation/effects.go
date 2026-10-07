package compensation

import "fmt"

// inverse 是一个已登记的逆操作。apply 在补偿流程中被调用：
// 返回 error 表示逆操作业务失败（计入补偿失败汇总）；
// panic 由补偿流程捕获并转换为补偿失败记录，绝不允许穿透。
// 对象列表用于补偿失败后的污染标记；执行成本为 O(1)，不遍历历史记录。
type inverse struct {
	step     int
	entry    uint64
	objects  []string
	apply    func() error
	failNow  bool
	panicNow bool
}

// run 执行逆操作，应用故障注入并把 panic 转换为 error（带 panicked 标记）。
func (inv *inverse) run() (ok bool, panicked bool, reason string) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
			panicked = true
			reason = fmt.Sprintf("inverse panic at step %d: %v", inv.step, r)
		}
	}()
	if inv.panicNow {
		panic(fmt.Sprintf("injected inverse panic at step %d", inv.step))
	}
	if inv.failNow {
		return false, false, fmt.Sprintf("injected inverse failure at step %d", inv.step)
	}
	if err := inv.apply(); err != nil {
		return false, false, err.Error()
	}
	return true, false, ""
}

// effectAndRegister 在同一个不可分割的处理单元内完成「子操作生效 + 逆操作登记」：
// 全程持有被修改对象（或链接两端对象）的写锁，中间不存在可失败的边界，
// 因而不可能出现「已生效未登记」或「已登记未生效」的中间状态。
func (e *Executor) effectAndRegister(op SubOp, step int, stack *[]*inverse) (string, error) {
	g := e.graph
	entry := g.nextEntry()

	switch op.Kind {
	case OpSetProperties:
		o, ok := g.object(op.ObjectID)
		if !ok {
			return "", fmt.Errorf("object %q not found", op.ObjectID)
		}
		o.mu.Lock()
		defer o.mu.Unlock()

		old := make(map[string]any, len(op.Sets))
		existed := make(map[string]bool, len(op.Sets))
		keys := make([]string, 0, len(op.Sets))
		for k := range op.Sets {
			keys = append(keys, k)
		}
		for _, k := range keys {
			v, ex := o.props[k]
			old[k], existed[k] = v, ex
		}

		// 生效：改属性、时钟戳、版本。
		for k, v := range op.Sets {
			o.props[k] = v
		}
		o.clock++
		o.version++

		objID := op.ObjectID
		inv := &inverse{
			step: step, entry: entry, objects: []string{objID},
			failNow: op.InjectInverseFailure, panicNow: op.InjectInversePanic,
			apply: func() error {
				oo, ok2 := g.object(objID)
				if !ok2 {
					return fmt.Errorf("object %q vanished during compensation", objID)
				}
				oo.mu.Lock()
				defer oo.mu.Unlock()
				for _, k2 := range keys {
					if existed[k2] {
						oo.props[k2] = old[k2]
					} else {
						delete(oo.props, k2)
					}
				}
				oo.clock--
				oo.version--
				return nil
			},
		}
		*stack = append(*stack, inv)
		return fmt.Sprintf("set %d props on %s (entry #%d)", len(op.Sets), op.ObjectID, entry), nil

	case OpCreateLink:
		g.mu.Lock()
		defer g.mu.Unlock()
		if l, ok := g.links[op.LinkID]; ok && l.alive {
			return "", fmt.Errorf("link %q already exists", op.LinkID)
		}

		objs := endpointObjects(g, op.From, op.To)
		for _, o := range objs {
			o.mu.Lock()
		}
		defer func() {
			for _, o := range objs {
				o.mu.Unlock()
			}
		}()

		g.links[op.LinkID] = &Link{
			id: op.LinkID, from: op.From, to: op.To, typ: op.LinkType, version: 1, alive: true,
		}
		for _, o := range objs {
			o.clock++
			o.version++
		}

		linkID, from, to := op.LinkID, op.From, op.To
		inv := &inverse{
			step: step, entry: entry, objects: nonEmpty(from, to),
			failNow: op.InjectInverseFailure, panicNow: op.InjectInversePanic,
			apply: func() error {
				g.mu.Lock()
				defer g.mu.Unlock()
				ll, ok2 := g.links[linkID]
				if !ok2 {
					return fmt.Errorf("link %q vanished during compensation", linkID)
				}
				if !ll.alive {
					return fmt.Errorf("link %q already deleted during compensation", linkID)
				}
				os2 := endpointObjects(g, ll.from, ll.to)
				for _, o := range os2 {
					o.mu.Lock()
				}
				defer func() {
					for _, o := range os2 {
						o.mu.Unlock()
					}
				}()
				ll.alive = false
				for _, o := range os2 {
					o.clock--
					o.version--
				}
				return nil
			},
		}
		*stack = append(*stack, inv)
		return fmt.Sprintf("create link %s %s->%s (entry #%d)", linkID, from, to, entry), nil

	case OpDeleteLink:
		g.mu.Lock()
		defer g.mu.Unlock()
		l, ok := g.links[op.LinkID]
		if !ok || !l.alive {
			return "", fmt.Errorf("link %q not found", op.LinkID)
		}
		objs := endpointObjects(g, l.from, l.to)
		for _, o := range objs {
			o.mu.Lock()
		}
		defer func() {
			for _, o := range objs {
				o.mu.Unlock()
			}
		}()

		l.alive = false
		l.version++
		for _, o := range objs {
			o.clock++
		}

		linkID, from, to, typ := op.LinkID, l.from, l.to, l.typ
		inv := &inverse{
			step: step, entry: entry, objects: nonEmpty(from, to),
			failNow: op.InjectInverseFailure, panicNow: op.InjectInversePanic,
			apply: func() error {
				g.mu.Lock()
				defer g.mu.Unlock()
				ll, ok2 := g.links[linkID]
				if !ok2 {
					ll = &Link{id: linkID, from: from, to: to, typ: typ}
					g.links[linkID] = ll
				}
				os2 := endpointObjects(g, ll.from, ll.to)
				for _, o := range os2 {
					o.mu.Lock()
				}
				defer func() {
					for _, o := range os2 {
						o.mu.Unlock()
					}
				}()
				if ll.alive {
					return fmt.Errorf("link %q unexpectedly alive during compensation", linkID)
				}
				ll.alive = true
				ll.version++
				for _, o := range os2 {
					o.clock--
				}
				return nil
			},
		}
		*stack = append(*stack, inv)
		return fmt.Sprintf("delete link %s (entry #%d)", linkID, entry), nil

	case OpHook:
		if _, ok := g.object(op.ObjectID); !ok {
			return "", fmt.Errorf("object %q not found", op.ObjectID)
		}
		// 钩子是只读级联校验，不改业务状态，版本/时钟均不变；
		// 逆操作是无操作，但仍登记并占用一个全局登记编号，
		// 从而保证登记编号跨流程不重复、不缺失。
		objID := op.ObjectID
		inv := &inverse{
			step: step, entry: entry, objects: []string{objID},
			failNow: op.InjectInverseFailure, panicNow: op.InjectInversePanic,
			apply: func() error { return nil },
		}
		*stack = append(*stack, inv)
		return fmt.Sprintf("hook %s on %s (entry #%d)", op.Hook, objID, entry), nil

	default:
		return "", fmt.Errorf("unknown op kind %d", op.Kind)
	}
}

func nonEmpty(ids ...string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// endpointObjects 调用方须持有 g.mu；按 ID 排序返回端点对象，保证多动作锁序一致。
func endpointObjects(g *Graph, from, to string) []*Object {
	out := make([]*Object, 0, 2)
	for _, id := range nonEmpty(from, to) {
		if o, ok := g.objects[id]; ok {
			out = append(out, o)
		}
	}
	return out
}
