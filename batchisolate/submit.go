package batchisolate

import "errors"

// 递归裁决状态：solve 的三态返回。
const (
	resFailed  = 0 // 该批未能整体交付（已继续隔离，子记录均已裁决）
	resOK      = 1 // 该批整批成功交付
	resAborted = 2 // 预算耗尽，本批及本分支内未裁决记录已记 Budget
)

type submitState struct {
	iso       *Isolator
	snapshot  map[string]struct{} // Submit 开始时的已知毒丸表快照
	reasons   map[string]Reason   // id -> 死信原因；缺失表示已交付
	newPoison []string            // 本次 Submit 判定出的新毒丸，判定顺序
	seenNew   map[string]struct{}
	calls     int
	lastPerm  bool // 最近一次 attempt 的错误性质：true=永久，false=瞬时耗尽
}

// Submit 提交一批编号进行隔离写入。
//
// ids 长度须在 1..100000 之间，且全部为非空、互不相同的字符串。
// 返回按原序排列的交付清单、死信清单及本次 Submit 的 sink 调用次数。
// 参数非法优先于已关闭报错；被拒绝的 Submit 不会调用 sink，也不会
// 改变已知毒丸表。
func (iso *Isolator) Submit(ids []string) (*Result, error) {
	if n := len(ids); n < 1 || n > 100000 {
		return nil, errInvalidArgs
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			return nil, errInvalidArgs
		}
		if _, dup := seen[id]; dup {
			return nil, errInvalidArgs
		}
		seen[id] = struct{}{}
	}

	iso.mu.Lock()
	if iso.closed {
		iso.mu.Unlock()
		return nil, errClosed
	}
	iso.wg.Add(1)
	snapshot := append([]string(nil), iso.table...)
	iso.mu.Unlock()
	defer iso.wg.Done()

	// 防御性拷贝，避免调用方在处理过程中修改入参切片。
	input := append([]string(nil), ids...)

	st := &submitState{
		iso:       iso,
		snapshot:  make(map[string]struct{}, len(snapshot)),
		reasons:   make(map[string]Reason, len(input)),
		seenNew:   make(map[string]struct{}),
		newPoison: make([]string, 0),
	}
	for _, id := range snapshot {
		st.snapshot[id] = struct{}{}
	}

	// 1) 摘出已知毒丸（不产生 sink 调用），其余保持原序构成待处理批。
	pending := make([]string, 0, len(input))
	for _, id := range input {
		if _, known := st.snapshot[id]; known {
			st.reasons[id] = Known
			continue
		}
		pending = append(pending, id)
	}

	// 2) 按规则递归隔离。
	st.solve(pending, false)

	// 3) 按判定顺序将新毒丸整体并入全局已知表（FIFO 淘汰）。
	if len(st.newPoison) > 0 {
		iso.mergePoison(st.newPoison)
	}

	// 4) 依原始入参顺序重建交付/死信清单，保证不重不漏且各自保序。
	res := &Result{Calls: st.calls, Delivered: []string{}, Dead: []DeadLetter{}}
	for _, id := range input {
		if reason, dead := st.reasons[id]; dead {
			res.Dead = append(res.Dead, DeadLetter{ID: id, Reason: reason})
		} else {
			res.Delivered = append(res.Delivered, id)
		}
	}
	return res, nil
}

// solve 对批 ids 执行隔离递归。
//
// certain 为真表示调用方已能确定本批包含毒丸：不调用 sink，直接视为
// 永久失败（perm=true）继续拆分。返回 resFailed/resOK/resAborted。
func (st *submitState) solve(ids []string, certain bool) int {
	n := len(ids)
	if n == 0 {
		return resOK
	}

	perm := true
	if !certain {
		ok, aborted := st.attempt(ids)
		if aborted {
			st.markBudget(ids)
			return resAborted
		}
		if ok {
			return resOK
		}
		perm = st.lastPerm
	}

	if n == 1 {
		id := ids[0]
		if perm {
			st.reasons[id] = Poison
			if _, exists := st.seenNew[id]; !exists {
				if _, known := st.snapshot[id]; !known {
					st.seenNew[id] = struct{}{}
					st.newPoison = append(st.newPoison, id)
				}
			}
		} else {
			st.reasons[id] = Exhausted
		}
		return resFailed
	}

	// 左半取前 ceil(n/2) 个。
	mid := (n + 1) / 2
	left, right := ids[:mid], ids[mid:]

	lok := st.solve(left, false)
	if lok == resAborted {
		// 左分支已把自身未裁决记录记 Budget；右半尚未触及，由本层记 Budget。
		st.markBudget(right)
		return resAborted
	}

	rres := st.solve(right, perm && lok == resOK)
	if rres == resAborted {
		return resAborted
	}
	return resFailed
}

// attempt 对整批最多尝试 R+1 次 sink 调用。
// 返回 (是否整批成功, 是否因预算中止)。中止时不写任何裁决。
func (st *submitState) attempt(ids []string) (ok, aborted bool) {
	for try := 0; try <= st.iso.r; try++ {
		if st.calls >= st.iso.cmax {
			return false, true
		}
		st.calls++
		st.iso.calls.Add(1)
		err := st.iso.sink(ids)
		if err == nil {
			return true, false
		}
		st.lastPerm = !errors.Is(err, ErrTransient)
		if st.lastPerm {
			return false, false
		}
	}
	return false, false
}

// markBudget 将尚未裁决的记录统一记为 Budget。
func (st *submitState) markBudget(ids []string) {
	for _, id := range ids {
		if _, decided := st.reasons[id]; !decided {
			st.reasons[id] = Budget
		}
	}
}

// mergePoison 在锁内按判定顺序整体并入新毒丸：已在表中不改变位置，
// 表满时先淘汰最早追加者。
func (iso *Isolator) mergePoison(newPoison []string) {
	iso.mu.Lock()
	defer iso.mu.Unlock()
	present := make(map[string]struct{}, len(iso.table))
	for _, id := range iso.table {
		present[id] = struct{}{}
	}
	for _, id := range newPoison {
		if _, exists := present[id]; exists {
			continue
		}
		if iso.km == 0 {
			continue
		}
		if len(iso.table) >= iso.km {
			evicted := iso.table[0]
			iso.table = iso.table[1:]
			delete(present, evicted)
		}
		iso.table = append(iso.table, id)
		present[id] = struct{}{}
	}
}
