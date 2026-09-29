package ontology

import (
	"log/slog"
	"sync"
	"sync/atomic"
)

// Store 是并发安全的本体存储：读操作永远基于已发布的不可变快照，重命名以原子替换提交。
type Store struct {
	snap atomic.Pointer[Graph]
	mu   sync.Mutex
	log  *slog.Logger
	// failPoint 在暂存完成、提交之前被调用；返回错误时模拟重命名中断并触发回滚。
	failPoint func(oldName, newName string) error
}

// NewStore 从初始图创建存储，并拒绝校验不通过（悬空引用 / 半改状态）的图。
func NewStore(g *Graph, log *slog.Logger) (*Store, error) {
	if g == nil {
		g = &Graph{}
	}
	if log == nil {
		log = slog.Default()
	}
	if g.Pending != nil {
		recovered, err := recoverPending(g)
		if err != nil {
			return nil, err
		}
		g = recovered
	}
	if err := verifyGraph(g, nil); err != nil {
		return nil, err
	}
	s := &Store{log: log}
	s.snap.Store(g)
	return s, nil
}

// Snapshot 返回当前已发布图快照。
func (s *Store) Snapshot() *Graph { return s.snap.Load() }

// RenameObjectType 将 oldName 的对象类型重命名为 newName，同步更新全图所有引用；
// 整体成功或整体回滚，被拒绝时不改变任何类型。
func (s *Store) RenameObjectType(oldName, newName string) (*ObjectType, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.snap.Load()
	target := current.Lookup(oldName)
	if target == nil {
		s.log.Warn("rename rejected: old name not found",
			slog.String("old_name", oldName), slog.String("new_name", newName),
			slog.String("reason", errorKind(ErrTypeNotFound)))
		return nil, withDetail(ErrTypeNotFound, "old_name="+oldName)
	}
	if other := current.Lookup(newName); other != nil {
		s.log.Warn("rename rejected: new name conflicts",
			slog.String("old_name", oldName), slog.String("new_name", newName),
			slog.String("rid", other.RID), slog.String("reason", errorKind(ErrNameConflict)))
		return nil, withDetail(ErrNameConflict, "new_name="+newName)
	}

	staged := cloneGraph(current)
	staged.Pending = &pendingRename{RID: target.RID, OldName: oldName, NewName: newName}

	updated := 0
	walkRefs(staged, func(loc refLocation, ref string) {
		if ref != oldName {
			return
		}
		rewriteRef(staged, loc, newName)
		updated++
		s.log.Info("reference updated",
			slog.String("location", loc.String()), slog.String("old_ref", oldName), slog.String("new_ref", newName))
	})
	for _, t := range staged.Types {
		if t.RID == target.RID {
			t.Name = newName
		}
	}

	s.log.Info("rename staged",
		slog.String("rid", target.RID), slog.String("old_name", oldName), slog.String("new_name", newName),
		slog.Int("references_updated", updated))

	if s.failPoint != nil {
		if err := s.failPoint(oldName, newName); err != nil {
			s.log.Error("rename interrupted before commit; rolling back",
				slog.String("rid", target.RID), slog.String("old_name", oldName), slog.String("new_name", newName),
				slog.String("cause", err.Error()))
			return nil, err
		}
	}

	if err := verifyGraph(staged, staged.Pending); err != nil {
		s.log.Error("rename rejected by staged verification; rolled back",
			slog.String("rid", target.RID), slog.String("old_name", oldName), slog.String("new_name", newName),
			slog.String("reason", errorKind(err)))
		return nil, err
	}

	staged.Pending = nil
	s.snap.Store(staged)
	committed := staged.Lookup(newName)
	s.log.Info("rename committed",
		slog.String("rid", committed.RID), slog.String("old_name", oldName), slog.String("new_name", newName),
		slog.Int("references_updated", updated))
	return committed, nil
}

func rewriteRef(g *Graph, loc refLocation, newName string) {
	switch loc.Kind {
	case "property.type":
		for _, t := range g.Types {
			if t.RID != loc.TypeRID {
				continue
			}
			for i := range t.Properties {
				if t.Properties[i].Name == loc.Property {
					t.Properties[i].TypeRef = newName
				}
			}
		}
	case "link.source", "link.target":
		for _, t := range g.Types {
			if t.RID != loc.TypeRID {
				continue
			}
			for i := range t.Links {
				if t.Links[i].Name != loc.Link {
					continue
				}
				if loc.Kind == "link.source" {
					t.Links[i].SourceType = newName
				} else {
					t.Links[i].TargetType = newName
				}
			}
		}
	default:
		a := findAction(g, loc.Action)
		if a == nil {
			return
		}
		switch loc.Kind {
		case "action.param":
			for i := range a.Params {
				if a.Params[i].Name == loc.Property {
					a.Params[i].TypeRef = newName
				}
			}
		case "action.inputs":
			a.Inputs[parseIndex(loc.Property)] = newName
		case "action.creates":
			a.Creates[parseIndex(loc.Property)] = newName
		case "action.reads":
			a.Reads[parseIndex(loc.Property)] = newName
		case "action.updates":
			a.Updates[parseIndex(loc.Property)] = newName
		case "action.deletes":
			a.Deletes[parseIndex(loc.Property)] = newName
		}
	}
}

func findAction(g *Graph, name string) *Action {
	for _, a := range g.Actions {
		if a.Name == name {
			return a
		}
	}
	return nil
}

func parseIndex(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func errorKind(err error) string {
	if ve, ok := err.(*VerificationError); ok {
		return ve.Kind
	}
	return "unknown"
}

func withDetail(base *VerificationError, detail string) *VerificationError {
	cp := *base
	cp.Detail = detail
	return &cp
}

// verifyGraph 校验图：无悬空引用；重命名后无旧名残留；旧名不可再解析；无半改状态。
// renamed 非 nil 时按一次重命名结果进行额外判定。
func verifyGraph(g *Graph, renamed *pendingRename) error {
	seen := make(map[string]string, len(g.Types))
	for _, t := range g.Types {
		if prev, dup := seen[t.Name]; dup {
			return withDetail(ErrNameConflict, "name="+t.Name+" rids="+prev+","+t.RID)
		}
		seen[t.Name] = t.RID
	}

	var dangling, residual string
	danglingAt, residualAt := -1, -1
	count := 0
	walkRefs(g, func(loc refLocation, ref string) {
		if ref == "" {
			return
		}
		if _, ok := seen[ref]; !ok {
			if dangling == "" {
				dangling = ref
				danglingAt = count
			}
		}
		if renamed != nil && ref == renamed.OldName {
			if residual == "" {
				residual = ref
				residualAt = count
			}
		}
		count++
	})

	if renamed != nil {
		if g.Pending == nil {
			return withDetail(ErrHalfApplied, "rid="+renamed.RID+" missing pending marker")
		}
		if g.Lookup(renamed.OldName) != nil {
			return withDetail(ErrStaleName, "old_name="+renamed.OldName)
		}
		if g.Lookup(renamed.NewName) == nil {
			return withDetail(ErrHalfApplied, "rid="+renamed.RID+" new_name="+renamed.NewName+" missing")
		}
		if residual != "" {
			return withDetail(ErrResidualOldName, "old_name="+residual+" ref_index="+itoa(residualAt))
		}
	}
	if dangling != "" {
		return withDetail(ErrDanglingReference, "ref="+dangling+" ref_index="+itoa(danglingAt))
	}
	return nil
}

// recoverPending 处理加载时发现的未决重命名：
// 原状态仍完好（旧名存在且无悬空）则回滚暂存；目标类型改名到位且引用一致则补提交；否则判定为不可恢复的半改状态并拒绝。
func recoverPending(g *Graph) (*Graph, error) {
	p := g.Pending
	if t := g.Lookup(p.OldName); t != nil && t.RID == p.RID {
		cp := cloneGraph(g)
		cp.Pending = nil
		if err := verifyGraph(cp, nil); err == nil {
			return cp, nil
		}
	}
	if err := verifyGraph(g, p); err == nil {
		cp := cloneGraph(g)
		cp.Pending = nil
		return cp, nil
	}
	return nil, withDetail(ErrHalfApplied,
		"rid="+p.RID+" old_name="+p.OldName+" new_name="+p.NewName)
}
