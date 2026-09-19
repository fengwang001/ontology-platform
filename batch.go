package ontology

import "fmt"

// OpKind 批量操作的种类。
type OpKind int

const (
	// OpLink 建链。
	OpLink OpKind = iota
	// OpUnlink 断链。
	OpUnlink
	// OpDeleteObject 删除对象（Source 字段为被删对象）。
	OpDeleteObject
)

func (k OpKind) String() string {
	switch k {
	case OpLink:
		return "LINK"
	case OpUnlink:
		return "UNLINK"
	case OpDeleteObject:
		return "DELETE_OBJECT"
	default:
		return "UNKNOWN"
	}
}

// BatchOp 描述批量中的一条操作。
type BatchOp struct {
	Kind     OpKind
	LinkType string
	Source   ObjectKey
	Target   ObjectKey
}

// LinkOp 构造建链操作。
func LinkOp(ltName string, src, tgt ObjectKey) BatchOp {
	return BatchOp{Kind: OpLink, LinkType: ltName, Source: src, Target: tgt}
}

// UnlinkOp 构造断链操作。
func UnlinkOp(ltName string, src, tgt ObjectKey) BatchOp {
	return BatchOp{Kind: OpUnlink, LinkType: ltName, Source: src, Target: tgt}
}

// DeleteOp 构造删除对象操作。
func DeleteOp(key ObjectKey) BatchOp {
	return BatchOp{Kind: OpDeleteObject, Source: key}
}

// runOpsLocked 顺序执行批内操作，返回失败序号；不做快照，由调用方保证回滚。
// 批内语义与逐条调用原语一致：重复建链报错、先建后删净效果为空、
// 批内建立的链可被同批的级联删除一并结算。
func (s *Store) runOpsLocked(ops []BatchOp) (int, error) {
	for i, op := range ops {
		var err error
		switch op.Kind {
		case OpLink:
			err = s.linkLocked(op.LinkType, op.Source, op.Target)
		case OpUnlink:
			err = s.unlinkLocked(op.LinkType, op.Source, op.Target)
		case OpDeleteObject:
			err = s.deleteLocked(op.Source)
		default:
			err = fmt.Errorf("unknown op kind %d", op.Kind)
		}
		if err != nil {
			return i, err
		}
	}
	return -1, nil
}

// ApplyBatch 原子地执行一批混合操作：任一条失败则整批回滚，
// 返回的 BatchError 指明失败操作在批内的序号。
func (s *Store) ApplyBatch(ops []BatchOp) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := s.snapshotLocked()
	if i, err := s.runOpsLocked(ops); err != nil {
		s.restoreLocked(snap)
		return &BatchError{Index: i, Op: ops[i], Err: err}
	}
	return nil
}

// Tx 收集一组操作，在 Commit 时原子生效并校验必选性约束。
type Tx struct {
	store *Store
	ops   []BatchOp
}

// Begin 开启一个事务。
func (s *Store) Begin() *Tx {
	return &Tx{store: s}
}

// Link 在事务中记录一条建链操作。
func (t *Tx) Link(ltName string, src, tgt ObjectKey) *Tx {
	t.ops = append(t.ops, LinkOp(ltName, src, tgt))
	return t
}

// Unlink 在事务中记录一条断链操作。
func (t *Tx) Unlink(ltName string, src, tgt ObjectKey) *Tx {
	t.ops = append(t.ops, UnlinkOp(ltName, src, tgt))
	return t
}

// DeleteObject 在事务中记录一条删除对象操作。
func (t *Tx) DeleteObject(key ObjectKey) *Tx {
	t.ops = append(t.ops, DeleteOp(key))
	return t
}

// Commit 原子应用全部操作，随后校验必选性约束；
// 任一失败都整体回滚。必选性违约通过 RequiredError 一次报全。
func (t *Tx) Commit() error {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	snap := t.store.snapshotLocked()
	if i, err := t.store.runOpsLocked(t.ops); err != nil {
		t.store.restoreLocked(snap)
		return &BatchError{Index: i, Op: t.ops[i], Err: err}
	}
	if violations := t.store.checkRequiredLocked(); len(violations) > 0 {
		t.store.restoreLocked(snap)
		return &RequiredError{Violations: violations}
	}
	return nil
}

// checkRequiredLocked 收集全部必选性违约，按 LinkType 与对象排序，一次报全。
func (s *Store) checkRequiredLocked() []RequiredViolation {
	var out []RequiredViolation
	for _, ltName := range s.sortedLinkTypeNames() {
		lt := s.linkTypes[ltName]
		if lt.SourceRequired {
			for _, obj := range s.objectsOfTypeLocked(lt.SourceType) {
				if len(s.fwd[ltName][obj]) == 0 {
					out = append(out, RequiredViolation{LinkType: ltName, Side: "source", Object: obj})
				}
			}
		}
		if lt.TargetRequired {
			for _, obj := range s.objectsOfTypeLocked(lt.TargetType) {
				if len(s.bwd[ltName][obj]) == 0 {
					out = append(out, RequiredViolation{LinkType: ltName, Side: "target", Object: obj})
				}
			}
		}
	}
	return out
}
