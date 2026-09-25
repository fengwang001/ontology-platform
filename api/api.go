// Package api 是对外门面：New / Insert / Read / MaskColumn / SelfCheck。
// 依赖 table（经 table 依赖 mask），依赖方向单向。
package api

import (
	"errors"
	"fmt"
	"strings"

	"ontology/mask"
	"ontology/table"
)

// Row 是对外的行类型（table.Row 的别名，写入载原始值，读出载掩码值）。
type Row = table.Row

// 三类可判定哨兵错误，互不相同。
var (
	ErrUnknownRole   = mask.ErrUnknownRole
	ErrUnknownColumn = mask.ErrUnknownColumn
	ErrInvalidID     = table.ErrInvalidID
)

// API 持有一张内存表。
type API struct{ t *table.Table }

// New 创建空实例。
func New() *API { return &API{t: table.New()} }

// Insert 插入一行；ID < 0 返回 ErrInvalidID 且不留痕。
func (a *API) Insert(r Row) error { return a.t.Insert(r) }

// Read 返回该角色下全部行（ID 升序）的脱敏视图；未知角色整体失败。
func (a *API) Read(role string) ([]Row, error) { return a.t.Read(role) }

// MaskColumn 按角色与列分派掩码纯函数。
func (a *API) MaskColumn(role, column, raw string) (string, error) {
	return mask.MaskColumn(role, column, raw)
}

// SelfCheck 在独立的内部实例上执行内置操作序列，核验四条不变量。
// 只读自身临时状态，可并发调用；任一核验失败返回可判定错误。
func (a *API) SelfCheck() error {
	t := table.New()
	if err := t.Insert(Row{ID: 1, Name: "张", Card: "1234567890123456", Phone: "13800138000"}); err != nil {
		return err
	}
	if err := t.Insert(Row{ID: 2, Name: "李四", Card: "999", Phone: "10086"}); err != nil {
		return err
	}

	// 不变量 1+3：三角色视图与规则逐字段一致。
	want := map[string][]Row{
		"admin":   {{ID: 1, Name: "张", Card: "1234567890123456", Phone: "13800138000"}, {ID: 2, Name: "李四", Card: "999", Phone: "10086"}},
		"analyst": {{ID: 1, Name: "张", Card: "************3456", Phone: "*******8000"}, {ID: 2, Name: "李*", Card: "***", Phone: "*0086"}},
		"none":    {{ID: 1}, {ID: 2}},
	}
	for role, w := range want {
		got, err := t.Read(role)
		if err != nil {
			return fmt.Errorf("selfcheck read %s: %w", role, err)
		}
		if len(got) != len(w) {
			return fmt.Errorf("selfcheck %s: row count %d != %d", role, len(got), len(w))
		}
		for i := range w {
			if got[i] != w[i] {
				return fmt.Errorf("selfcheck %s row %d: got %+v want %+v", role, i, got[i], w[i])
			}
		}
	}

	// 不变量 2：analyst/none 视图不得泄露被掩掉的原始子串。
	leaks := []string{"123456789012", "1380013", "999", "李四"}
	for _, role := range []string{"analyst", "none"} {
		rows, err := t.Read(role)
		if err != nil {
			return err
		}
		for _, r := range rows {
			for _, leak := range leaks {
				for _, col := range []string{r.Name, r.Card, r.Phone} {
					if strings.Contains(col, leak) {
						return fmt.Errorf("selfcheck leak: %s shows %q", role, leak)
					}
				}
			}
		}
	}

	// 不变量 4：三类被拒操作不留痕，拒绝后状态不变、可继续正常使用。
	before, err := t.Read("admin")
	if err != nil {
		return err
	}
	if err := t.Insert(Row{ID: -1}); !errors.Is(err, table.ErrInvalidID) {
		return fmt.Errorf("selfcheck: want ErrInvalidID, got %v", err)
	}
	if _, err := t.Read("root"); !errors.Is(err, mask.ErrUnknownRole) {
		return fmt.Errorf("selfcheck: want ErrUnknownRole, got %v", err)
	}
	if _, err := mask.MaskColumn("admin", "ssn", "x"); !errors.Is(err, mask.ErrUnknownColumn) {
		return fmt.Errorf("selfcheck: want ErrUnknownColumn, got %v", err)
	}
	after, err := t.Read("admin")
	if err != nil {
		return err
	}
	if len(after) != len(before) {
		return errors.New("selfcheck: state changed by rejected ops")
	}
	for i := range before {
		if after[i] != before[i] {
			return errors.New("selfcheck: state changed by rejected ops")
		}
	}
	return t.Insert(Row{ID: 3, Name: "王五", Card: "12345", Phone: "10000"})
}
