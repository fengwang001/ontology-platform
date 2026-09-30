package ontology

import (
	"errors"
	"fmt"
	"math/bits"
)

// 语句级错误原因，彼此可区分，可用 errors.Is 判定。
var (
	// ErrInvalidRange 表示 lo > hi 的非法区间。
	ErrInvalidRange = errors.New("非法区间: lo > hi")
	// ErrUnknownColumn 表示过滤或赋值引用了表结构中不存在的列。
	ErrUnknownColumn = errors.New("未知列")
	// ErrOverflow 表示某一行的新值计算发生 int64 溢出。
	ErrOverflow = errors.New("int64 算术溢出")
	// ErrUniqueViolation 表示语句结束时唯一索引上存在重复键。
	ErrUniqueViolation = errors.New("唯一索引冲突")
)

// CmpOp 是过滤条件的比较运算符。
type CmpOp int

const (
	OpEq CmpOp = iota // =
	OpNe              // !=
	OpLt              // <
	OpLe              // <=
	OpGt              // >
	OpGe              // >=
)

// Filter 是针对非索引列的可选过滤条件，按更新前的行值求值。
type Filter struct {
	Column string
	Op     CmpOp
	Value  int64
}

func (f Filter) match(row map[string]int64) bool {
	v := row[f.Column]
	switch f.Op {
	case OpEq:
		return v == f.Value
	case OpNe:
		return v != f.Value
	case OpLt:
		return v < f.Value
	case OpLe:
		return v <= f.Value
	case OpGt:
		return v > f.Value
	case OpGe:
		return v >= f.Value
	}
	return false
}

// AssignOp 是赋值运算符。
type AssignOp int

const (
	// AssignMul 将索引列乘以一个常量。
	AssignMul AssignOp = iota
	// AssignAdd 将索引列加上一个常量。
	AssignAdd
	// AssignSet 将其他列设置为一个常量。
	AssignSet
)

// Assignment 描述对单列的赋值。
// 目标为索引列时只允许 AssignMul / AssignAdd；目标为其他列时只允许 AssignSet。
type Assignment struct {
	Column  string
	Op      AssignOp
	Operand int64
}

// UpdateStmt 是一条批量更新语句：沿索引列的 [Lo, Hi) 区间扫描，
// 用可选的 Filter 过滤后，对选中的行应用 Assign。
type UpdateStmt struct {
	Lo, Hi int64
	Filter *Filter
	Assign Assignment
}

// UpdateResult 是语句的执行结果。
type UpdateResult struct {
	// Updated 是实际更新的行数。
	Updated int
	// Examined 是扫描考察的索引条目数，等于更新前落在 [Lo, Hi) 内的条目数。
	Examined int
}

// Update 以「先按更新前快照选出全部行再逐行更新」的语义执行批量更新。
//
// 恰好一次的保证：访问路径是沿索引的区间扫描，扫描先把 [Lo, Hi) 内的
// 条目（按索引顺序）一次性收集为快照，之后只按主键回访这些行；
// 更新中被移到扫描前方、或移出区间再移入的行不会被再次考察。
//
// 唯一性在整条语句结束时判定，中途的暂时重复不算冲突。
// 发生溢出、唯一冲突等错误时，已应用的修改按逆序逐项回滚，
// 表与索引恢复到语句开始前的状态。
func (t *Table) Update(stmt UpdateStmt) (UpdateResult, error) {
	if stmt.Lo > stmt.Hi {
		return UpdateResult{}, fmt.Errorf("%w: lo=%d hi=%d", ErrInvalidRange, stmt.Lo, stmt.Hi)
	}
	if err := t.validate(stmt); err != nil {
		return UpdateResult{}, err
	}
	if stmt.Lo == stmt.Hi {
		return UpdateResult{}, nil // 空区间，更新零行
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// 沿索引的区间扫描：收集更新前落在区间内的条目快照。
	entries := t.scanRange(stmt.Lo, stmt.Hi)
	result := UpdateResult{Examined: len(entries)}

	type undo struct {
		pk     int64
		oldKey int64
		oldRow map[string]int64
	}
	var undos []undo
	rollback := func() {
		for i := len(undos) - 1; i >= 0; i-- {
			u := undos[i]
			row := t.rows[u.pk]
			t.removeEntry(row[t.indexColumn], u.pk)
			for c, v := range u.oldRow {
				row[c] = v
			}
			t.insertEntry(u.oldKey, u.pk)
		}
	}

	for _, e := range entries {
		row := t.rows[e.pk]
		if stmt.Filter != nil && !stmt.Filter.match(row) {
			continue
		}
		newVal, overflow := applyAssign(row[stmt.Assign.Column], stmt.Assign)
		if overflow {
			rollback()
			return UpdateResult{}, fmt.Errorf("%w: 行 pk=%d 列 %q", ErrOverflow, e.pk, stmt.Assign.Column)
		}
		oldRow := make(map[string]int64, len(row))
		for c, v := range row {
			oldRow[c] = v
		}
		if stmt.Assign.Column == t.indexColumn {
			t.removeEntry(e.key, e.pk)
			row[t.indexColumn] = newVal
			t.insertEntry(newVal, e.pk)
		} else {
			row[stmt.Assign.Column] = newVal
		}
		undos = append(undos, undo{pk: e.pk, oldKey: e.key, oldRow: oldRow})
		result.Updated++
	}

	// 唯一性在语句结束时判定；仅当索引列被修改时才可能产生冲突。
	if t.unique && stmt.Assign.Column == t.indexColumn {
		if dup, pk1, pk2 := t.findDuplicate(); dup {
			rollback()
			return UpdateResult{}, fmt.Errorf("%w: 键 %d 被行 pk=%d 与 pk=%d 同时占用", ErrUniqueViolation, t.rows[pk1][t.indexColumn], pk1, pk2)
		}
	}
	return result, nil
}

// validate 校验语句引用的列与赋值形态。
func (t *Table) validate(stmt UpdateStmt) error {
	if _, ok := t.columns[stmt.Assign.Column]; !ok {
		return fmt.Errorf("%w: 赋值列 %q", ErrUnknownColumn, stmt.Assign.Column)
	}
	if stmt.Assign.Column == t.indexColumn {
		if stmt.Assign.Op != AssignMul && stmt.Assign.Op != AssignAdd {
			return fmt.Errorf("索引列 %q 只支持乘或加常量", stmt.Assign.Column)
		}
	} else if stmt.Assign.Op != AssignSet {
		return fmt.Errorf("非索引列 %q 只支持设置为常量", stmt.Assign.Column)
	}
	if stmt.Filter != nil {
		if _, ok := t.columns[stmt.Filter.Column]; !ok {
			return fmt.Errorf("%w: 过滤列 %q", ErrUnknownColumn, stmt.Filter.Column)
		}
		if stmt.Filter.Column == t.indexColumn {
			return fmt.Errorf("过滤列 %q 不能是索引列", stmt.Filter.Column)
		}
	}
	return nil
}

// applyAssign 计算新值并报告是否溢出。
func applyAssign(old int64, a Assignment) (int64, bool) {
	switch a.Op {
	case AssignMul:
		return mulOverflow(old, a.Operand)
	case AssignAdd:
		return addOverflow(old, a.Operand)
	case AssignSet:
		return a.Operand, false
	}
	return 0, true
}

func addOverflow(a, b int64) (int64, bool) {
	s := a + b
	// 溢出当且仅当 a、b 同号且结果异号。
	if (a^s)&(b^s) < 0 {
		return 0, true
	}
	return s, false
}

func mulOverflow(a, b int64) (int64, bool) {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	// 低 64 位的符号扩展必须等于高 64 位，否则积超出 int64。
	if hi != uint64(int64(lo)>>63) {
		return 0, true
	}
	return int64(lo), false
}

// findDuplicate 在唯一索引上查找相邻重复键。
func (t *Table) findDuplicate() (bool, int64, int64) {
	for i := 1; i < len(t.index); i++ {
		if t.index[i-1].key == t.index[i].key {
			return true, t.index[i-1].pk, t.index[i].pk
		}
	}
	return false, 0, 0
}
