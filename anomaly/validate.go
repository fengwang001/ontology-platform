package anomaly

import (
	"fmt"
	"sort"
)

// 错误码。按题面规定的优先级，一次只返回第一个错误。
const (
	ErrTxnID       = "ErrTxnID"       // 事务编号为零或重复
	ErrStatus      = "ErrStatus"      // 状态既非 committed 也非 aborted
	ErrReadVersion = "ErrReadVersion" // 读引用了不存在的版本
	ErrOrder       = "ErrOrder"       // 版本次序不符合构成规则
	ErrTooMany     = "ErrTooMany"     // 事务数超过 12
)

type invalidHistory struct {
	code string
	msg  string
}

func (e *invalidHistory) Error() string { return e.msg }

// keyIndex 给键分配稳定的小整数编号，避免在 map 键里存字符串。
type keyIndex struct {
	names []string
	index map[string]int
}

func newKeyIndex() *keyIndex {
	return &keyIndex{index: map[string]int{}}
}

func (ki *keyIndex) get(k string) int {
	if id, ok := ki.index[k]; ok {
		return id
	}
	id := len(ki.names)
	ki.names = append(ki.names, k)
	ki.index[k] = id
	return id
}

type validated struct {
	txns       map[int]*Txn
	committed  map[int]bool
	keys       *keyIndex
	writeCount map[[2]int]int // (txn, keyID) -> 写次数
	lastWrite  map[[2]int]int // (txn, keyID) -> 最后一次写的 Seq
}

func validate(h History) (*validated, error) {
	// 1. 事务编号为零或重复。
	seen := make(map[int]bool, len(h.Txns))
	for i := range h.Txns {
		id := h.Txns[i].ID
		if id == 0 {
			return nil, &invalidHistory{ErrTxnID, "transaction id 0 is reserved for the initial version"}
		}
		if seen[id] {
			return nil, &invalidHistory{ErrTxnID, fmt.Sprintf("duplicate transaction id %d", id)}
		}
		seen[id] = true
	}

	txns := make(map[int]*Txn, len(h.Txns))
	committed := make(map[int]bool, len(h.Txns))
	keys := newKeyIndex()
	wc := map[[2]int]int{}
	last := map[[2]int]int{}

	// 2. 状态合法。
	for i := range h.Txns {
		t := &h.Txns[i]
		switch t.Status {
		case Committed:
			committed[t.ID] = true
		case Aborted:
		default:
			return nil, &invalidHistory{ErrStatus, fmt.Sprintf("transaction %d has invalid status %q", t.ID, t.Status)}
		}
		txns[t.ID] = t
	}

	// 统计写次数与最后一次写（Seq 依据事务内操作顺序，与输入排列无关）。
	for i := range h.Txns {
		t := &h.Txns[i]
		for _, op := range t.Ops {
			if op.Read == nil {
				k := keys.get(op.Key)
				wc[[2]int{t.ID, k}]++
				last[[2]int{t.ID, k}] = wc[[2]int{t.ID, k}]
			}
		}
	}

	// 3. 读引用的版本必须存在：初始版本 (0,0)，或某事务确实写出的版本。
	for i := range h.Txns {
		t := &h.Txns[i]
		for _, op := range t.Ops {
			if op.Read == nil {
				continue // 写操作没有 Read 字段
			}
			v := *op.Read
			if v.Txn == 0 {
				if v.Seq != 0 {
					return nil, &invalidHistory{ErrReadVersion, fmt.Sprintf("transaction %d reads nonexistent initial version %+v", t.ID, v)}
				}
				continue
			}
			k := keys.get(op.Key)
			if v.Seq < 1 || v.Seq > wc[[2]int{v.Txn, k}] {
				return nil, &invalidHistory{ErrReadVersion, fmt.Sprintf("transaction %d reads nonexistent version (txn=%d,seq=%d) of key %q", t.ID, v.Txn, v.Seq, op.Key)}
			}
		}
	}

	// 4. 版本次序校验。收集所有出现过的键，按名字排序保证确定性。
	keySet := map[string]bool{}
	for k := range h.Order {
		keySet[k] = true
	}
	for i := range h.Txns {
		for _, op := range h.Txns[i].Ops {
			keySet[op.Key] = true
		}
	}
	keyNames := make([]string, 0, len(keySet))
	for k := range keySet {
		keyNames = append(keyNames, k)
	}
	sort.Strings(keyNames)

	for _, name := range keyNames {
		k := keys.get(name)
		ids := make([]int, 0)
		for id := range committed {
			if _, ok := last[[2]int{id, k}]; ok {
				ids = append(ids, id)
			}
		}
		sort.Ints(ids)
		expected := make([]Version, 0, len(ids))
		expectedSet := map[Version]bool{{0, 0}: true}
		for _, id := range ids {
			v := Version{id, last[[2]int{id, k}]}
			expected = append(expected, v)
			expectedSet[v] = true
		}

		order, given := h.Order[name]
		if len(expected) == 0 {
			// 无已提交写：允许缺省，或只含初始版本。
			if !given {
				continue
			}
			if len(order) == 1 && order[0] == (Version{0, 0}) {
				continue
			}
			return nil, &invalidHistory{ErrOrder, fmt.Sprintf("version order of key %q must contain only the initial version", name)}
		}

		if !given || len(order) != len(expected)+1 {
			return nil, &invalidHistory{ErrOrder, fmt.Sprintf("version order of key %q is missing committed last-write versions", name)}
		}
		if order[0] != (Version{0, 0}) {
			return nil, &invalidHistory{ErrOrder, fmt.Sprintf("version order of key %q must start with the initial version", name)}
		}
		seenV := map[Version]bool{{0, 0}: true}
		for _, v := range order[1:] {
			if seenV[v] {
				return nil, &invalidHistory{ErrOrder, fmt.Sprintf("version %+v appears more than once in order of key %q", v, name)}
			}
			seenV[v] = true
			if !expectedSet[v] {
				if _, ok := txns[v.Txn]; ok && !committed[v.Txn] {
					return nil, &invalidHistory{ErrOrder, fmt.Sprintf("order of key %q contains version of aborted transaction %d", name, v.Txn)}
				}
				if seq, ok := last[[2]int{v.Txn, k}]; ok && v.Seq != seq {
					return nil, &invalidHistory{ErrOrder, fmt.Sprintf("order of key %q contains non-last-write version %+v", name, v)}
				}
				return nil, &invalidHistory{ErrOrder, fmt.Sprintf("order of key %q contains unexpected version %+v", name, v)}
			}
		}
	}

	// 5. 事务数超过 12（最后检查）。
	if len(h.Txns) > 12 {
		return nil, &invalidHistory{ErrTooMany, fmt.Sprintf("history has %d transactions, limit is 12", len(h.Txns))}
	}

	return &validated{
		txns:       txns,
		committed:  committed,
		keys:       keys,
		writeCount: wc,
		lastWrite:  last,
	}, nil
}
