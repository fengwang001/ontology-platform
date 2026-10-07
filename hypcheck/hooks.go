package hypcheck

import "sort"

// hookState 持有钩子发布版本与“钩子绑定到动作类型”的版本集合。
// 绑定存储值为 map[hookID]Phase；发布存储值为 HookVersion。
type hookState struct {
	published *snapshotMap[HookVersion]
	bindings  *snapshotMap[bindingSet]
}

// bindingSet 记录某 (type, hook) 绑定在当时所处阶段；空串表示已解绑。
type bindingSet struct {
	phase Phase
}

func newHookState() *hookState {
	return &hookState{
		published: newSnapshotMap[HookVersion](),
		bindings:  newSnapshotMap[bindingSet](),
	}
}

func bindingKey(typeID, hookID string) string { return typeID + "\x01" + hookID }

func (h *hookState) apply(e Event) {}

// resolveHooks 解析 typeID 在 t 时刻生效的全部前置/后置钩子（按 hookID 排序）。
// 存储尚无数据时即当时没有任何钩子（空世界是确定性的）；
// 真正的历史缺口（t 早于压实切点）由 Engine.Precheck 在入口统一判定为 E2。
// hookView 是一次预检内的钩子只读视图。
type hookView struct {
	published *mapView[HookVersion]
	bindings  *mapView[bindingSet]
}

func (h *hookState) view(stats *ProbeStats) *hookView {
	return &hookView{published: h.published.with(stats), bindings: h.bindings.with(stats)}
}

func (h *hookView) resolveHooks(typeID string, t Timestamp) (hooks []ResolvedHook, covered bool) {
	bRoot, bCovered := h.bindings.rootAt(t)
	pRoot, _ := h.published.rootAt(t)
	if !bCovered {
		return nil, true
	}
	prefix := typeID + "\x01"
	var ids []string
	phaseOf := map[string]Phase{}
	avlRange(bRoot, prefix, nodeUpperBound(typeID), func(k string, b bindingSet) {
		if b.phase == "" {
			return
		}
		hookID := k[len(prefix):]
		ids = append(ids, hookID)
		phaseOf[hookID] = b.phase
	})
	sort.Strings(ids)
	for _, id := range ids {
		hv, found := avlGet(pRoot, id)
		if !found {
			// 绑定指向当时尚未发布的钩子：当时该绑定悬空，视为无此钩子。
			continue
		}
		hooks = append(hooks, ResolvedHook{
			HookID:  id,
			Phase:   phaseOf[id],
			Version: hv.v.Version,
			Spec:    hv.v.Spec,
		})
	}
	return hooks, true
}

// avlRange 对键落在 [lo, hi) 的节点按序回调，复杂度 O(log K + 输出数)。
func avlRange[T any](n *avlNode[T], lo, hi string, fn func(string, T)) {
	if n == nil {
		return
	}
	if n.key >= lo {
		avlRange(n.left, lo, hi, fn)
	}
	if n.key >= lo && n.key < hi {
		fn(n.key, n.val.v)
	}
	if n.key < hi {
		avlRange(n.right, lo, hi, fn)
	}
}
