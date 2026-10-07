package diff

// diff.go 是比对组件的对外入口，固定按三级优先级组织判定顺序：
//
//  1. 快照损坏（构造期校验，Compare 时复核）——没有可信输入则一切免谈；
//  2. 结构层冲突——结构层是实例层的解释前提，结构判定互斥时实例层
//     口径无从谈起；
//  3. 实例层口径不可对齐——结构本身成立，但其变化使某些旧取值无法
//     按新口径解读。
//
// Compare 对入参只读；同一份快照被任意多个 goroutine 并发调用，
// 结果完全一致（所有索引在 Builder.Build 时预计算，比对过程零写入）。

// AuditSink 可选地接收每次比对的输入、输出与判定依据，供复核存档。
type AuditSink interface {
	Record(entry AuditEntry)
}

// AuditEntry 是一次比对的完整审计记录。
type AuditEntry struct {
	OldSnap  *Snapshot
	NewSnap  *Snapshot
	Diff     *Diff
	Err      *CompareError
	Evidence []string
}

// Compare 比对两份快照，先给出结构层差异，再在其口径上给出实例层差异。
func Compare(oldSnap, newSnap *Snapshot) (*Diff, *CompareError) {
	return CompareWithAudit(oldSnap, newSnap, nil)
}

// CompareWithAudit 与 Compare 相同，但会把判定依据写入 sink。
func CompareWithAudit(oldSnap, newSnap *Snapshot, sink AuditSink) (*Diff, *CompareError) {
	var evidence []string
	fail := func(e *CompareError) (*Diff, *CompareError) {
		if sink != nil {
			sink.Record(AuditEntry{oldSnap, newSnap, nil, e, evidence})
		}
		return nil, e
	}
	if oldSnap == nil || newSnap == nil || oldSnap.index == nil || newSnap.index == nil {
		return fail(corrupt("", "snapshot was not built via diff.NewBuilder().Build()"))
	}

	sd, err := diffStructural(oldSnap, newSnap)
	if err != nil {
		return fail(err)
	}
	evidence = append(evidence, "structural diff complete")

	align, err := buildAlignment(oldSnap, newSnap, sd)
	if err != nil {
		return fail(err)
	}

	id, err := diffInstances(oldSnap, newSnap, align)
	if err != nil {
		return fail(err)
	}

	out := &Diff{Structural: sd, Instance: id}
	if sink != nil {
		sink.Record(AuditEntry{oldSnap, newSnap, out, nil, evidence})
	}
	return out, nil
}
