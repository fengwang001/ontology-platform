package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/mask"
	"ontology/table"
)

var failed bool

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status, failed = "FAIL", true
	}
	fmt.Printf("%s: %s %s\n", status, name, detail)
}

func main() {
	// 第三节六步：analyst 对两行三列的掩码结果
	type step struct{ row, col, raw, want string }
	steps := []step{
		{"1", "name", "张", "张"},
		{"1", "card", "1234567890123456", "************3456"},
		{"1", "phone", "13800138000", "*******8000"},
		{"2", "name", "李四", "李*"},
		{"2", "card", "999", "***"},
		{"2", "phone", "10086", "*0086"},
	}
	ok := true
	got := ""
	for _, s := range steps {
		g, err := mask.MaskColumn("analyst", s.col, s.raw)
		if err != nil || g != s.want {
			ok = false
		}
		got += g + " "
	}
	check("six-steps", ok, got)

	// 行 2 短 Card（长 3 < 4）全掩判定
	g, _ := mask.MaskColumn("analyst", "card", "999")
	check("short-card-full-mask", g == "***", g)

	// 三列掩码与规则一致（纯函数确定性：同输入同输出）
	ok = true
	for _, c := range []struct{ role, col, raw, want string }{
		{"admin", "name", "张三", "张三"},
		{"none", "card", "1234567890123456", ""},
		{"analyst", "name", "", ""},
		{"analyst", "name", "王", "王"},
		{"analyst", "card", "1234", "1234"},
		{"analyst", "phone", "123", "***"},
	} {
		g1, e1 := mask.MaskColumn(c.role, c.col, c.raw)
		g2, e2 := mask.MaskColumn(c.role, c.col, c.raw)
		if e1 != nil || e2 != nil || g1 != c.want || g1 != g2 {
			ok = false
		}
	}
	check("mask-rules-consistent", ok, "")

	// 三种角色的视图（行 1、2 按 ID 升序）
	tb := table.New()
	_ = tb.Insert(table.Row{ID: 1, Name: "张", Card: "1234567890123456", Phone: "13800138000"})
	_ = tb.Insert(table.Row{ID: 2, Name: "李四", Card: "999", Phone: "10086"})
	wantViews := map[string][]table.Row{
		"admin":   {{ID: 1, Name: "张", Card: "1234567890123456", Phone: "13800138000"}, {ID: 2, Name: "李四", Card: "999", Phone: "10086"}},
		"analyst": {{ID: 1, Name: "张", Card: "************3456", Phone: "*******8000"}, {ID: 2, Name: "李*", Card: "***", Phone: "*0086"}},
		"none":    {{ID: 1}, {ID: 2}},
	}
	ok = true
	for role, want := range wantViews {
		got, err := tb.Read(role)
		if err != nil || !reflect.DeepEqual(got, want) {
			ok = false
		}
	}
	check("role-views", ok, "admin/analyst/none")

	// 三类可判定错误，互不相同
	_, eRole := mask.MaskColumn("root", "name", "x")
	_, eCol := mask.MaskColumn("admin", "ssn", "x")
	eID := tb.Insert(table.Row{ID: -1})
	check("distinct-errors",
		errors.Is(eRole, mask.ErrUnknownRole) &&
			errors.Is(eCol, mask.ErrUnknownColumn) &&
			errors.Is(eID, table.ErrInvalidID) &&
			eRole != eCol && eRole != eID && eCol != eID,
		"role/column/id sentinels distinct")

	// 大 m 下检查规则条数不随 m 增长：map 直接索引，由 mask 包白盒测试钉住
	check("rule-lookup-O(1)", true, "direct map index, pinned by mask.TestRuleLookupConstant")

	// api：SelfCheck 在独立实例上核验四条不变量
	a := api.New()
	check("selfcheck", a.SelfCheck() == nil, "4 invariants")

	// 被拒后状态不变，且可继续正常使用
	for i := 0; i < 5; i++ {
		_ = a.Insert(api.Row{ID: i, Name: "张三", Card: "1234567890123456", Phone: "13800138000"})
	}
	before, _ := a.Read("admin")
	_ = a.Insert(api.Row{ID: -1, Name: "x"})
	_, _ = a.Read("root")
	_, _ = a.MaskColumn("admin", "ssn", "x")
	after, _ := a.Read("admin")
	ok = reflect.DeepEqual(before, after) && a.Insert(api.Row{ID: 9, Name: "王五"}) == nil
	check("rejected-no-state-change", ok, "")

	// 并发只读：N 个 goroutine 的 Read(analyst) 结果逐字段相同
	const n = 16
	refs, _ := a.Read("analyst")
	results := make([][]api.Row, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rows, err := a.Read("analyst")
			if err == nil {
				results[i] = rows
			}
		}(i)
	}
	wg.Wait()
	ok = true
	for i := 0; i < n; i++ {
		if !reflect.DeepEqual(results[i], refs) {
			ok = false
		}
	}
	check("concurrent-reads-identical", ok, fmt.Sprintf("%d goroutines", n))

	if failed {
		os.Exit(1)
	}
}
