package index

import "fmt"

// Op 是条件运算符。
type Op int

const (
	OpEq     Op = iota // 等于
	OpIn               // 属于集合
	OpLt               // 小于
	OpLe               // 小于等于
	OpGt               // 大于
	OpGe               // 大于等于
	OpIsNull           // 为空
)

func (op Op) String() string {
	switch op {
	case OpEq:
		return "="
	case OpIn:
		return "IN"
	case OpLt:
		return "<"
	case OpLe:
		return "<="
	case OpGt:
		return ">"
	case OpGe:
		return ">="
	case OpIsNull:
		return "IS NULL"
	}
	return "?"
}

// Cond 是单列条件。Eq/范围运算带 1 个值，In 带 n 个值，IsNull 不带值。
type Cond struct {
	Column string
	Op     Op
	Values []Value
}

func Eq(col string, v Value) Cond { return Cond{Column: col, Op: OpEq, Values: []Value{v}} }
func In(col string, vs ...Value) Cond {
	return Cond{Column: col, Op: OpIn, Values: append([]Value(nil), vs...)}
}
func Lt(col string, v Value) Cond { return Cond{Column: col, Op: OpLt, Values: []Value{v}} }
func Le(col string, v Value) Cond { return Cond{Column: col, Op: OpLe, Values: []Value{v}} }
func Gt(col string, v Value) Cond { return Cond{Column: col, Op: OpGt, Values: []Value{v}} }
func Ge(col string, v Value) Cond { return Cond{Column: col, Op: OpGe, Values: []Value{v}} }
func IsNull(col string) Cond      { return Cond{Column: col, Op: OpIsNull} }

func (c Cond) String() string {
	if c.Op == OpIsNull {
		return fmt.Sprintf("%s IS NULL", c.Column)
	}
	if c.Op == OpIn {
		s := ""
		for i, v := range c.Values {
			if i > 0 {
				s += ","
			}
			s += v.String()
		}
		return fmt.Sprintf("%s IN {%s}", c.Column, s)
	}
	return fmt.Sprintf("%s %s %v", c.Column, c.Op, c.Values[0])
}

// Query 是一组条件的合取；同列多条件取交集。
type Query struct {
	Conds []Cond
}

func (q Query) String() string {
	if len(q.Conds) == 0 {
		return "(no conditions)"
	}
	s := ""
	for i, c := range q.Conds {
		if i > 0 {
			s += " AND "
		}
		s += c.String()
	}
	return s
}
