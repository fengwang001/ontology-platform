package diff

// CompareError 是比对过程中产生的、带固定优先级分类的错误。
type CompareError struct {
	// Phase 为错误类别，取值为三类互斥的固定优先级：
	//   PhaseSnapshotCorrupt     优先级 1（最高）
	//   PhaseStructuralConflict  优先级 2
	//   PhaseInstanceAlignment   优先级 3
	Phase ErrorPhase
	// Which 标识损坏/冲突来自哪一份快照（"old" / "new" / ""）。
	Which string
	msg   string
}

// ErrorPhase 是三类错误的固定优先级枚举。
type ErrorPhase int

const (
	PhaseNone ErrorPhase = iota
	// PhaseSnapshotCorrupt：快照本身结构性损坏（重复 UID、悬空引用、
	// 墓碑与现存记录矛盾、取值与声明类型不符等），不具备比对前提。
	PhaseSnapshotCorrupt
	// PhaseStructuralConflict：单看每份快照都合法，但结构层判定
	// 对同一元素同时给出互斥结论（例如同一 UID 既被判定重命名
	// 又被判定删除+新增）。
	PhaseStructuralConflict
	// PhaseInstanceAlignment：结构层差异成立，但其变化使得旧快照
	// 中的具体取值无法按新快照口径解读（如取值类型变为 Opaque），
	// 实例层无法确定比对口径。
	PhaseInstanceAlignment
)

func (e *CompareError) Error() string {
	if e.Which != "" {
		return e.Phase.String() + " (" + e.Which + "): " + e.msg
	}
	return e.Phase.String() + ": " + e.msg
}

func (p ErrorPhase) String() string {
	switch p {
	case PhaseSnapshotCorrupt:
		return "snapshot-corrupt"
	case PhaseStructuralConflict:
		return "structural-conflict"
	case PhaseInstanceAlignment:
		return "instance-alignment"
	default:
		return "none"
	}
}

func corrupt(which, msg string) *CompareError {
	return &CompareError{Phase: PhaseSnapshotCorrupt, Which: which, msg: msg}
}

func structuralConflict(msg string) *CompareError {
	return &CompareError{Phase: PhaseStructuralConflict, msg: msg}
}

func alignmentError(msg string) *CompareError {
	return &CompareError{Phase: PhaseInstanceAlignment, msg: msg}
}
