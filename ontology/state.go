package ontology

import "sort"

// partState 是单个资产分区的运行时状态。
type partState struct {
	materialized bool
	version      int64
	running      bool
	// consumed 记录该分区上一次成功物化开始时各输入分区的版本。
	consumed map[partKey]int64
}

// run 记录一次物化运行在开始时刻冻结的输入快照。
type run struct {
	id       int64
	target   partKey
	finished bool
	success  bool
	inputs   map[partKey]int64
}

func (p *Planner) state(k partKey) *partState {
	s := p.parts[k]
	if s == nil {
		s = &partState{consumed: map[partKey]int64{}}
		p.parts[k] = s
	}
	return s
}

// inputRefs 返回某下游分区此刻实际读取的全部输入分区（已去重、已排序）。
// 调用方需持锁。
func (p *Planner) inputRefs(downName string, d int) []partKey {
	var refs []partKey
	seen := map[partKey]bool{}
	for _, e := range p.inputs[downName] {
		for _, uPart := range e.consumed(p.assets[e.up], d) {
			k := partKey{e.up, uPart}
			if !seen[k] {
				seen[k] = true
				refs = append(refs, k)
			}
		}
	}
	sortPartKeys(p.assets, refs)
	return refs
}

func sortPartKeys(assets map[string]*asset, ks []partKey) {
	sort.Slice(ks, func(i, j int) bool {
		di, dj := assets[ks[i].asset].depth, assets[ks[j].asset].depth
		if di != dj {
			return di < dj
		}
		if ks[i].asset != ks[j].asset {
			return ks[i].asset < ks[j].asset
		}
		return ks[i].part < ks[j].part
	})
}

func refsToAPI(ks []partKey) []PartitionRef {
	out := make([]PartitionRef, 0, len(ks))
	for _, k := range ks {
		out = append(out, PartitionRef{Asset: k.asset, Partition: k.part})
	}
	return out
}

func sortKeys(ks []partKey) {
	sort.Slice(ks, func(i, j int) bool {
		if ks[i].asset != ks[j].asset {
			return ks[i].asset < ks[j].asset
		}
		return ks[i].part < ks[j].part
	})
}
