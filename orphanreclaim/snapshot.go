package orphanreclaim

import "sort"

// Snapshot 是引擎在某一全局次序点上的可比对状态。
type Snapshot struct {
	At        int64
	Alive     map[string]Generation          // 存活对象 -> 当前待回收代
	Since     map[string]int64               // 入代判定时刻
	Deadline  map[string]int64               // 到期时刻
	InEdges   map[string]map[string][]string // target -> source -> 入边类型列表（排序）
	OutEdges  map[string]map[string][]string // source -> target -> 出边类型列表（排序）
	Gen1Queue []string
	Gen2Queue []string
}

// Snapshot 读取与变更共用同一把锁，因此它必然对应某个全局次序点，
// 朴素模型只需在重放完全相同的操作序列后逐字段比对。
func (r *Reclaimer) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	s := Snapshot{
		At:       r.clock(),
		Alive:    map[string]Generation{},
		Since:    map[string]int64{},
		Deadline: map[string]int64{},
		InEdges:  map[string]map[string][]string{},
		OutEdges: map[string]map[string][]string{},
	}
	for id, o := range r.objects {
		s.Alive[id] = o.gen
		if o.gen != GenNone {
			s.Since[id] = o.since
			s.Deadline[id] = o.deadline
		}
		in := map[string][]string{}
		for src, types := range o.inLinks {
			in[src] = sortedKeys(types)
		}
		s.InEdges[id] = in
		out := map[string][]string{}
		for tgt, types := range o.outLinks {
			out[tgt] = sortedKeys(types)
		}
		s.OutEdges[id] = out
	}
	for _, o := range r.gen1.inner.order {
		s.Gen1Queue = append(s.Gen1Queue, o.id)
	}
	for _, o := range r.gen2.inner.order {
		s.Gen2Queue = append(s.Gen2Queue, o.id)
	}
	sort.Strings(s.Gen1Queue)
	sort.Strings(s.Gen2Queue)
	return s
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
