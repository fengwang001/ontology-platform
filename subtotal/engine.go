package subtotal

import (
	"sync"
)

const (
	opUpsert = "upsert"
	opDelete = "delete"
)

// Store 并发安全地维护明细行与三层分组计数/求和及变更日志。
// 读（View/Log/SelfCheck）使用读锁，可与其他读并发；提交使用写锁，
// 与一切访问互斥，因此被拒绝的提交不可能留下任何中间痕迹。
type Store struct {
	mu        sync.RWMutex
	maxDetail int // <=0 表示不限

	rows    map[string]Row
	details map[Key2]GroupStat
	sub1s   map[Key1]GroupStat
	total   GroupStat

	log []Change
	seq int64
}

// New 创建一个明细组数上限为 maxDetailGroups 的存储；<=0 表示不限。
func New(maxDetailGroups int) *Store {
	return &Store{
		maxDetail: maxDetailGroups,
		rows:      make(map[string]Row),
		details:   make(map[Key2]GroupStat),
		sub1s:     make(map[Key1]GroupStat),
	}
}

func reject(code RejectCode, reason string) error {
	return &RejectError{Code: code, Reason: reason}
}

func dupStr(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func dupRow(r Row) Row {
	r.Dim1 = dupStr(r.Dim1)
	r.Dim2 = dupStr(r.Dim2)
	return r
}

func dimVal(p *string) (string, bool) {
	if p == nil {
		return "", true
	}
	return *p, false
}

func key1Of(r Row) Key1 {
	v, null := dimVal(r.Dim1)
	return Key1{V: v, Null: null}
}

func key2Of(r Row) Key2 {
	a, an := dimVal(r.Dim1)
	b, bn := dimVal(r.Dim2)
	return Key2{A: a, ANull: an, B: b, BNull: bn}
}

func sameGroupAndAmount(a, b Row) bool {
	return a.Amount == b.Amount &&
		(a.Dim1 == nil) == (b.Dim1 == nil) &&
		(a.Dim1 == nil || *a.Dim1 == *b.Dim1) &&
		(a.Dim2 == nil) == (b.Dim2 == nil) &&
		(a.Dim2 == nil || *a.Dim2 == *b.Dim2)
}

func dimPtr(v string, null bool) *string {
	if null {
		return nil
	}
	s := v
	return &s
}

func add64(a, b int64) (int64, bool) {
	s := a + b
	if b > 0 && s < a {
		return 0, false
	}
	if b < 0 && s > a {
		return 0, false
	}
	return s, true
}

type deltaLeg struct {
	amount int64
	sign   int64
	k1     Key1
	k2     Key2
}

// Commit 原子地应用一条增量：op 为 "upsert" 插入或覆盖行，为 "delete" 撤回行。
// 成功返回本次产生的变更，顺序为按三层（明细→小计→总计），每层先负向后正向；
// 失败返回 *RejectError，且行集、三层状态与日志均不改变。
//
// 实现采用两阶段：先在影子状态上完整推演整条增量并生成日志，任何非法情形
// 都在触碰真实状态之前返回；只有全部成功才把影子结果一次性发布。
func (s *Store) Commit(op string, row Row) ([]Change, error) {
	if op != opUpsert && op != opDelete {
		return nil, reject(RejectMalformed, "op must be upsert or delete, got "+op)
	}
	if row.ID == "" {
		return nil, reject(RejectMalformed, "row id must not be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	old, existed := s.rows[row.ID]
	if op == opDelete && !existed {
		return nil, reject(RejectRowIDNotFound, "no live row with id "+row.ID)
	}
	if op == opUpsert && existed && sameGroupAndAmount(old, row) {
		// 内容完全一致的重复插入不是变更：不产生日志，也不动状态。
		return []Change{}, nil
	}

	// 影子状态：真实状态的浅拷贝（键/值均为不可变值类型，按项覆盖即可）。
	simDetails := make(map[Key2]GroupStat, len(s.details)+1)
	simSub1 := make(map[Key1]GroupStat, len(s.sub1s)+1)
	for k, v := range s.details {
		simDetails[k] = v
	}
	for k, v := range s.sub1s {
		simSub1[k] = v
	}
	simTotal := s.total

	legs := make([]deltaLeg, 0, 2)
	if existed {
		legs = append(legs, deltaLeg{old.Amount, -1, key1Of(old), key2Of(old)})
	}
	if op == opUpsert {
		legs = append(legs, deltaLeg{row.Amount, 1, key1Of(row), key2Of(row)})
	}

	changes := make([]Change, 0, 3*len(legs))
	seq := s.seq

	emit := func(layer int, k1 *Key1, k2 *Key2, grand bool, leg deltaLeg) error {
		dc, ds := leg.sign, leg.sign*leg.amount
		var (
			stat    GroupStat
			present bool
		)
		switch {
		case grand:
			stat, present = simTotal, true
		case k2 != nil:
			stat, present = simDetails[*k2]
		default:
			stat, present = simSub1[*k1]
		}

		nc, ok1 := add64(stat.Count, dc)
		ns, ok2 := add64(stat.Sum, ds)
		if !ok1 || !ok2 || nc < 0 {
			return reject(RejectAmountInvalid, "int64 count/sum overflow or negative count")
		}

		created := !present && nc != 0
		removed := present && nc == 0
		next := GroupStat{Count: nc, Sum: ns}
		switch {
		case grand:
			simTotal = next // 总计占位恒保留，即使计数归零
		case k2 != nil:
			if nc == 0 {
				delete(simDetails, *k2)
			} else {
				simDetails[*k2] = next
			}
		default:
			if nc == 0 {
				delete(simSub1, *k1)
			} else {
				simSub1[*k1] = next
			}
		}

		seq++
		c := Change{
			Seq:     seq,
			Op:      op,
			Layer:   layer,
			Grand:   grand,
			Count:   nc,
			Sum:     ns,
			Delta:   ds,
			CDelta:  dc,
			Created: created,
			Removed: removed,
		}
		if k2 != nil {
			c.Dim1 = dimPtr(k2.A, k2.ANull)
			c.Dim2 = dimPtr(k2.B, k2.BNull)
		} else if k1 != nil {
			c.Dim1 = dimPtr(k1.V, k1.Null)
		}
		changes = append(changes, c)
		return nil
	}

	// 层间严格按 明细(1) → 小计(2) → 总计(3) 推进；每层内部先撤回（负）后加入（正）。
	for _, layer := range []int{LayerDetail, LayerSub1, LayerTotal} {
		for idx := range legs {
			leg := legs[idx]
			var err error
			switch layer {
			case LayerDetail:
				k2 := leg.k2
				err = emit(layer, nil, &k2, false, leg)
			case LayerSub1:
				k1 := leg.k1
				err = emit(layer, &k1, nil, false, leg)
			default:
				err = emit(layer, nil, nil, true, leg)
			}
			if err != nil {
				// 此时只修改了影子状态与局部变量，真实状态、日志、序号原封不动。
				return nil, err
			}
		}
	}

	if s.maxDetail > 0 && len(simDetails) > s.maxDetail {
		return nil, reject(RejectDetailLimit, "accepting this upsert would create "+
			"detail group beyond limit")
	}

	// 全部推演成功，一次性发布。
	s.details, s.sub1s, s.total = simDetails, simSub1, simTotal
	s.seq = seq
	if op == opDelete {
		delete(s.rows, row.ID)
	} else {
		s.rows[row.ID] = dupRow(row)
	}
	s.log = append(s.log, changes...)

	return append([]Change(nil), changes...), nil
}
