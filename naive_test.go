package ontology

import (
	"errors"
	"fmt"
	"sort"
)

// naiveModel 是按题目规则逐行直译的“朴素参考模型”，
// 不与生产实现共享任何内部数据结构（仅复用纯函数表）。
type naiveModel struct {
	parent map[string]string
	locks  map[string]map[int64]Mode
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		parent: map[string]string{},
		locks:  map[string]map[int64]Mode{},
	}
}

func (n *naiveModel) exists(node string) bool {
	_, ok := n.parent[node]
	return ok
}

// ancestors 返回真祖先，顺序为从根往下。
func (n *naiveModel) ancestors(node string) []string {
	var rev []string
	for p := n.parent[node]; p != ""; p = n.parent[p] {
		rev = append(rev, p)
	}
	out := make([]string, len(rev))
	for i, a := range rev {
		out[len(rev)-1-i] = a
	}
	return out
}

// mutResult 是变更类操作（Register/Lock/Unlock/ReleaseAll）的统一结果。
// errKey 为空表示成功；mode 仅 Lock 成功时有值；detail 携带可比较的错误细节。
type mutResult struct {
	errKey string
	detail string
	mode   Mode
}

func errKey(err error) string {
	switch {
	case errors.Is(err, ErrInvalidTxn):
		return "InvalidTxn"
	case errors.Is(err, ErrInvalidMode):
		return "InvalidMode"
	case errors.Is(err, ErrEmptyNodeID):
		return "EmptyNodeID"
	case errors.Is(err, ErrNodeExists):
		return "NodeExists"
	case errors.Is(err, ErrParentNotExist):
		return "ParentNotExist"
	case errors.Is(err, ErrNodeNotExist):
		return "NodeNotExist"
	case errors.Is(err, ErrMissingIntent):
		return "MissingIntent"
	case errors.Is(err, ErrLockConflict):
		return "LockConflict"
	case errors.Is(err, ErrLockNotHeld):
		return "LockNotHeld"
	case errors.Is(err, ErrDescendantLocked):
		return "DescendantLocked"
	case err == nil:
		return ""
	default:
		return fmt.Sprintf("UNKNOWN(%v)", err)
	}
}

func sameMut(a, b mutResult) bool {
	return a.errKey == b.errKey && a.detail == b.detail && a.mode == b.mode
}

func (n *naiveModel) register(id, parent string) mutResult {
	if id == "" {
		return mutResult{errKey: "EmptyNodeID"}
	}
	if n.exists(id) {
		return mutResult{errKey: "NodeExists"}
	}
	if parent != "" && !n.exists(parent) {
		return mutResult{errKey: "ParentNotExist"}
	}
	n.parent[id] = parent
	n.locks[id] = map[int64]Mode{}
	return mutResult{}
}

func (n *naiveModel) lock(txn int64, node string, mode Mode) mutResult {
	if txn <= 0 {
		return mutResult{errKey: "InvalidTxn"}
	}
	if !validMode[mode] {
		return mutResult{errKey: "InvalidMode"}
	}
	if !n.exists(node) {
		return mutResult{errKey: "NodeNotExist"}
	}

	holders := n.locks[node]
	held, has := holders[txn]
	if has && atLeast(held, mode) {
		return mutResult{mode: held} // 无操作成功
	}

	target := mode
	if has {
		target = join(held, mode)
	}

	need := requiredIntent(target)
	for _, anc := range n.ancestors(node) {
		ah, ok := n.locks[anc][txn]
		if !ok || !atLeast(ah, need) {
			return mutResult{
				errKey: "MissingIntent",
				detail: fmt.Sprintf("ancestor=%s need=%s", anc, need),
			}
		}
	}

	var others []int64
	for other := range holders {
		if other != txn {
			others = append(others, other)
		}
	}
	sort.Slice(others, func(i, j int) bool { return others[i] < others[j] })
	for _, other := range others {
		if !canCoexist(target, holders[other]) {
			return mutResult{
				errKey: "LockConflict",
				detail: fmt.Sprintf("with=%d:%s", other, holders[other]),
			}
		}
	}

	holders[txn] = target
	return mutResult{mode: target}
}

func (n *naiveModel) unlock(txn int64, node string) mutResult {
	if txn <= 0 {
		return mutResult{errKey: "InvalidTxn"}
	}
	if !n.exists(node) {
		return mutResult{errKey: "NodeNotExist"}
	}
	if _, has := n.locks[node][txn]; !has {
		return mutResult{errKey: "LockNotHeld"}
	}

	var descs []string
	for cand, p := range n.parent {
		if cand == node {
			continue
		}
		for ; p != ""; p = n.parent[p] {
			if p == node {
				descs = append(descs, cand)
				break
			}
		}
	}
	sort.Strings(descs)
	for _, d := range descs {
		if md, ok := n.locks[d][txn]; ok {
			return mutResult{
				errKey: "DescendantLocked",
				detail: fmt.Sprintf("desc=%s:%s", d, md),
			}
		}
	}
	delete(n.locks[node], txn)
	return mutResult{}
}

func (n *naiveModel) releaseAll(txn int64) mutResult {
	if txn <= 0 {
		return mutResult{errKey: "InvalidTxn"}
	}
	for _, holders := range n.locks {
		delete(holders, txn)
	}
	return mutResult{}
}

type queryResult struct {
	errKey  string
	mode    Mode
	has     bool
	holders []Holder
}

func (n *naiveModel) holders(node string) queryResult {
	if !n.exists(node) {
		return queryResult{errKey: "NodeNotExist"}
	}
	var txns []int64
	for txn := range n.locks[node] {
		txns = append(txns, txn)
	}
	sort.Slice(txns, func(i, j int) bool { return txns[i] < txns[j] })
	res := queryResult{}
	for _, txn := range txns {
		res.holders = append(res.holders, Holder{Txn: txn, Mode: n.locks[node][txn]})
	}
	return res
}

// 生产侧结果转换。
func prodRegisterResult(err error) mutResult {
	return mutResult{errKey: errKey(err)}
}

func prodLockResult(mode Mode, err error) mutResult {
	r := mutResult{errKey: errKey(err)}
	if err == nil {
		r.mode = mode
		return r
	}
	var ie *AncestorIntentError
	if errors.As(err, &ie) {
		r.detail = fmt.Sprintf("ancestor=%s need=%s", ie.Ancestor, ie.Required)
	}
	var ce *ConflictError
	if errors.As(err, &ce) {
		r.detail = fmt.Sprintf("with=%d:%s", ce.ConflictTxn, ce.ConflictMode)
	}
	return r
}

func prodUnlockResult(err error) mutResult {
	r := mutResult{errKey: errKey(err)}
	var de *DescendantLockError
	if errors.As(err, &de) {
		r.detail = fmt.Sprintf("desc=%s:%s", de.Descendant, de.Mode)
	}
	return r
}
