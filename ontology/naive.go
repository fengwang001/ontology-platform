package ontology

import "sort"

// NaiveIndexModel 是独立实现的朴素索引重建模型：
// 它不信任平台的重建过程，只读取平台暴露的“原始写入流水”，
// 从头重放全部历史写入得到期望索引，用于随机交错序列的对照。
//
// 注意：朴素模型刻意线性扫描每条历史写入（O(历史总次数)），
// 与正式复核器每条目 O(1) 当前态读形成可测量的性能对照。
type NaiveIndexModel struct {
	entries map[Value]map[ObjectID]struct{}
}

// RebuildNaive 重放到 cutoffSeq（含）为止的全部相关写入。
func RebuildNaive(writes []Write, t TypeID, a AttrName, cutoffSeq int64) *NaiveIndexModel {
	m := &NaiveIndexModel{entries: map[Value]map[ObjectID]struct{}{}}
	for _, w := range writes {
		if w.Seq > cutoffSeq || w.Type != t || w.Attr != a {
			continue
		}
		for val, bucket := range m.entries {
			if _, ok := bucket[w.Object]; ok {
				delete(bucket, w.Object)
				if len(bucket) == 0 {
					delete(m.entries, val)
				}
			}
		}
		if w.Op == OpPut {
			if m.entries[w.Value] == nil {
				m.entries[w.Value] = map[ObjectID]struct{}{}
			}
			m.entries[w.Value][w.Object] = struct{}{}
		}
	}
	return m
}

// Query 返回按值命中的对象（有序，便于确定性比较）。
func (m *NaiveIndexModel) Query(v Value) []ObjectID {
	out := []ObjectID{}
	for o := range m.entries[v] {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// HistoryWrites 暴露原始写入流水（仅供朴素模型与测试，复核器不读取）。
func (p *Platform) HistoryWrites() []Write {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Write(nil), p.writeLog...)
}

// CurrentSeq 返回当前全局序号。
func (p *Platform) CurrentSeq() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seq
}

// DiffAgainstNaive 用平台当前可用索引与朴素模型（重放到当前 seq）逐条对照。
// 返回不一致条目清单；空切片表示完全一致。
func (p *Platform) DiffAgainstNaive(t TypeID, a AttrName) []string {
	p.mu.Lock()
	writes := append([]Write(nil), p.writeLog...)
	cut := p.seq
	st := p.indexes[indexKey{t: t, a: a}]
	var actual map[Value]map[ObjectID]entryMeta
	available := st != nil && st.status == StatusAvailable
	if available {
		actual = st.entries
	}
	p.mu.Unlock()

	naive := RebuildNaive(writes, t, a, cut)
	diffs := []string{}
	values := map[Value]bool{}
	for v := range naive.entries {
		values[v] = true
	}
	for v := range actual {
		values[v] = true
	}
	vs := make([]Value, 0, len(values))
	for v := range values {
		vs = append(vs, v)
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i] < vs[j] })
	for _, v := range vs {
		want := map[ObjectID]bool{}
		for _, o := range naive.Query(v) {
			want[o] = true
		}
		got := map[ObjectID]bool{}
		if available {
			for o := range actual[v] {
				got[o] = true
			}
		}
		for o := range want {
			if !got[o] {
				diffs = append(diffs, "missing value="+string(v)+" object="+string(o))
			}
		}
		for o := range got {
			if !want[o] {
				diffs = append(diffs, "extra value="+string(v)+" object="+string(o))
			}
		}
	}
	sort.Strings(diffs)
	return diffs
}

// InjectCorruption 仅供测试：人为篡改当前索引条目，证明复核不依赖重建执行日志。
func (p *Platform) InjectCorruption(t TypeID, a AttrName, obj ObjectID, fake Value) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.indexes[indexKey{t: t, a: a}]
	if st == nil || st.entries == nil {
		return
	}
	for val, bucket := range st.entries {
		if meta, ok := bucket[obj]; ok {
			delete(bucket, obj)
			if len(bucket) == 0 {
				delete(st.entries, val)
			}
			if st.entries[fake] == nil {
				st.entries[fake] = map[ObjectID]entryMeta{}
			}
			st.entries[fake][obj] = meta
			return
		}
	}
}

// TamperAuditDigest 仅供测试：破坏审计指纹而不触碰对象与索引内容。
func (p *Platform) TamperAuditDigest(t TypeID, a AttrName) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st, ok := p.indexes[indexKey{t: t, a: a}]; ok && st.audit != nil {
		st.audit.Digest = "0000000000000000"
	}
}
