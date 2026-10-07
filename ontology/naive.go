package ontology

import "sort"

// NaiveModel 是独立实现的朴素全量重放参照模型。
//
// 它不依赖引擎的任何内部结构：只记录初始状态与全部历史事件
// （单实例写入、批次及其生效与否），需要当前状态时从头完整重放。
// 测试用它与引擎恢复后的最终状态对照，验证恢复语义的正确性。
type NaiveModel struct {
	initial map[InstanceID]Instance
	events  []naiveEvent
}

type naiveEvent struct {
	writeID   *InstanceID // 单实例写入
	writeProp map[string]string
	batchID   BatchID
	batchMut  []Mutation
	effective bool // 该批次是否被（外部）认定为已生效
}

// NewNaiveModel 以给定初始实例集合创建参照模型。
func NewNaiveModel() *NaiveModel {
	return &NaiveModel{initial: make(map[InstanceID]Instance)}
}

// AddWrite 记录一次单实例写入（与 Engine.Write 语义一致）。
func (m *NaiveModel) AddWrite(id InstanceID, props map[string]string) {
	pid := id
	m.events = append(m.events, naiveEvent{writeID: &pid, writeProp: props})
}

// AddBatch 记录一个批次及其生效判定（与恢复归类结果一一对应）。
func (m *NaiveModel) AddBatch(id BatchID, muts []Mutation, effective bool) {
	cp := make([]Mutation, len(muts))
	copy(cp, muts)
	m.events = append(m.events, naiveEvent{batchID: id, batchMut: cp, effective: effective})
}

// State 从头全量重放全部历史事件，返回期望的当前状态（按 ID 排序）。
func (m *NaiveModel) State() []Instance {
	mem := make(map[InstanceID]Instance, len(m.initial))
	for k, v := range m.initial {
		mem[k] = v.Clone()
	}
	for _, ev := range m.events {
		switch {
		case ev.writeID != nil:
			in, ok := mem[*ev.writeID]
			if !ok {
				in = Instance{ID: *ev.writeID, Props: map[string]string{}}
			}
			in.Version++
			in.Props = mergeProps(in.Props, ev.writeProp)
			mem[in.ID] = in
		case ev.effective:
			for _, mut := range ev.batchMut {
				in, ok := mem[mut.Instance]
				if !ok {
					continue // 与引擎一致：批次只作用于已存在实例
				}
				in.Version++
				in.LastBatch = ev.batchID
				in.Props = mergeProps(in.Props, mut.Props)
				mem[in.ID] = in
			}
		}
	}
	ids := make([]string, 0, len(mem))
	for id := range mem {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	out := make([]Instance, 0, len(ids))
	for _, id := range ids {
		out = append(out, mem[InstanceID(id)].Clone())
	}
	return out
}
