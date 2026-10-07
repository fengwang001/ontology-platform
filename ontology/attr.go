package ontology

// SetAttr 写入对象实例的数值属性并增量维护相关视图，返回受影响的起点实例集合。
func (g *Graph) SetAttr(id, attr string, value int64) (map[string]struct{}, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	o, ok := g.objects[id]
	if !ok {
		return nil, errf(KindInstanceNotFound, "object %q", id)
	}
	oldVal, had := o.attrs[attr]
	o.attrs[attr] = value // 先写值；视图提交是内存原子操作，无需回滚属性（失败时下面恢复）
	type cand struct {
		vs       *viewState
		newsnap  *snapshot
		affected map[string]struct{}
		reason   string
	}
	cands := make([]cand, 0, len(g.views))
	for _, vs := range g.views {
		if vs.spec.Attr != attr {
			continue
		}
		refs := vs.snap.endRefs[id]
		if len(refs) == 0 {
			continue // 该实例当前不是任何起点的可达终点，与它无关的起点不受影响
		}
		ns := cloneSnapshot(vs.snap)
		ns.rescanCost = 0
		affected := map[string]struct{}{}
		for s := range refs {
			before := aggValue(ns.agg[s])
			info := ns.agg[s]
			switch {
			case !had || value > oldVal:
				// 新增或变大：仅与现有最大值比较一次。
				if !info.present || value > info.value {
					info.present = true
					info.value = value
					info.maxEnd = id
				}
			case value == oldVal:
				// 无变化。
			default:
				// 变小：仅当该终点恰好是当前最大值来源时才需重新确定。
				if info.maxEnd != id || !info.present {
					continue
				}
				if value < info.value {
					g.redetermine(vs, ns, s)
				} else {
					// 值不变（并列或持平）：maxEnd 仍有效。
					info.value = value
				}
			}
			if aggChanged(before, ns.agg[s]) {
				affected[s] = struct{}{}
			}
		}
		if vs.failNext {
			vs.failNext = false
			if had {
				o.attrs[attr] = oldVal
			} else {
				delete(o.attrs, attr)
			}
			return nil, errf(KindMaintenanceRollback,
				"view %q: injected maintenance failure at SetAttr %s.%s", vs.spec.Name, id, attr)
		}
		cands = append(cands, cand{
			vs:       vs,
			newsnap:  ns,
			affected: affected,
			reason: sprintf("attr %s %s: %d->%d; reverse-index %d reachable starts; %d results changed; redetermine only prior-max losers",
				attr, id, oldVal, value, len(refs), len(affected)),
		})
	}
	// 原子提交全部视图快照。
	total := map[string]struct{}{}
	for _, c := range cands {
		c.vs.snap = c.newsnap
		for s := range c.affected {
			total[s] = struct{}{}
		}
	}
	g.logChange(sprintf("SetAttr %s.%s=%d (was %d, present=%v)", id, attr, value, oldVal, had), total, nil)
	if g.logf != nil {
		for _, c := range cands {
			g.logf(sprintf("  view=%s basis=%s", c.vs.spec.Name, c.reason))
		}
	}
	return total, nil
}

// redetermine 为单个起点重新确定最大值来源。
// 考察范围严格限定为该起点当前真实可达的终点集合，不扫描任何无关实例。
func (g *Graph) redetermine(vs *viewState, ns *snapshot, start string) {
	info := &aggInfo{}
	for k := range ns.reach {
		if k.start != start {
			continue
		}
		ns.rescanCost++
		v, ok := g.objects[k.end].attrs[vs.spec.Attr]
		if !ok {
			continue
		}
		if !info.present || v > info.value {
			info.present = true
			info.value = v
			info.maxEnd = k.end
		}
	}
	ns.agg[start] = info
}

func cloneSnapshot(s *snapshot) *snapshot {
	c := &snapshot{
		reach:      make(map[reachKey]int64, len(s.reach)),
		agg:        make(map[string]*aggInfo, len(s.agg)),
		endRefs:    make(map[string]map[string]struct{}, len(s.endRefs)),
		rescanCost: s.rescanCost,
	}
	for k, v := range s.reach {
		c.reach[k] = v
	}
	for k, v := range s.agg {
		cc := *v
		c.agg[k] = &cc
	}
	for k, set := range s.endRefs {
		cp := make(map[string]struct{}, len(set))
		for x := range set {
			cp[x] = struct{}{}
		}
		c.endRefs[k] = cp
	}
	return c
}

type aggBefore struct {
	present bool
	value   int64
}

func aggValue(info *aggInfo) aggBefore {
	return aggBefore{present: info.present, value: info.value}
}

func aggChanged(b aggBefore, info *aggInfo) bool {
	return b.present != info.present || (b.present && b.value != info.value)
}
