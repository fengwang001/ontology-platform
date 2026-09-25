package api

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func seed(t *testing.T) *API {
	t.Helper()
	a := New()
	for _, r := range []Row{
		{ID: 1, Name: "张", Card: "1234567890123456", Phone: "13800138000"},
		{ID: 2, Name: "李四", Card: "999", Phone: "10086"},
	} {
		if err := a.Insert(r); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	return a
}

// TestViewsPerRole 钉住不变量 3：三角色视图逐字段比对。
func TestViewsPerRole(t *testing.T) {
	a := seed(t)
	want := map[string][]Row{
		"admin":   {{ID: 1, Name: "张", Card: "1234567890123456", Phone: "13800138000"}, {ID: 2, Name: "李四", Card: "999", Phone: "10086"}},
		"analyst": {{ID: 1, Name: "张", Card: "************3456", Phone: "*******8000"}, {ID: 2, Name: "李*", Card: "***", Phone: "*0086"}},
		"none":    {{ID: 1}, {ID: 2}},
	}
	for role, w := range want {
		got, err := a.Read(role)
		if err != nil {
			t.Fatalf("Read(%q): %v", role, err)
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("Read(%q) = %+v, want %+v", role, got, w)
		}
	}
}

// TestNoLeak 钉住不变量 2：analyst/none 输出不得含被掩掉的原始子串。
func TestNoLeak(t *testing.T) {
	a := seed(t)
	// 每行每列被掩掉的部分（保留部分不在此列）。
	maskedParts := []string{"123456789012", "1380013", "999", "四"}
	for _, role := range []string{"analyst", "none"} {
		rows, err := a.Read(role)
		if err != nil {
			t.Fatalf("Read(%q): %v", role, err)
		}
		for _, r := range rows {
			for _, col := range []string{r.Name, r.Card, r.Phone} {
				for _, part := range maskedParts {
					if strings.Contains(col, part) {
						t.Errorf("%s 视图 %q 泄露了被掩子串 %q", role, col, part)
					}
				}
			}
		}
	}
}

// TestRejectedNoStateChange 钉住不变量 4：三类被拒操作互不相同、不留痕、之后可正常使用。
func TestRejectedNoStateChange(t *testing.T) {
	a := seed(t)
	before, err := a.Read("admin")
	if err != nil {
		t.Fatal(err)
	}
	eID := a.Insert(Row{ID: -1, Name: "x"})
	_, eRole := a.Read("root")
	_, eCol := a.MaskColumn("admin", "ssn", "x")
	if !errors.Is(eID, ErrInvalidID) || !errors.Is(eRole, ErrUnknownRole) || !errors.Is(eCol, ErrUnknownColumn) {
		t.Fatalf("哨兵错误不匹配: %v %v %v", eID, eRole, eCol)
	}
	if eID == eRole || eID == eCol || eRole == eCol {
		t.Fatal("三类错误必须互不相同")
	}
	after, err := a.Read("admin")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("被拒操作改变了状态")
	}
	if err := a.Insert(Row{ID: 3, Name: "王五"}); err != nil {
		t.Fatalf("被拒后无法继续正常使用: %v", err)
	}
}

// TestConcurrentRead 钉住并发约束：N 个 goroutine 并发 Read(analyst)，结果逐字段相同。
func TestConcurrentRead(t *testing.T) {
	a := seed(t)
	for i := 3; i < 10; i++ {
		if err := a.Insert(Row{ID: i, Name: "张三", Card: "1234567890123456", Phone: "13800138000"}); err != nil {
			t.Fatal(err)
		}
	}
	refs, err := a.Read("analyst")
	if err != nil {
		t.Fatal(err)
	}
	const n = 32
	results := make([][]Row, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rows, err := a.Read("analyst")
			if err != nil {
				t.Errorf("并发 Read: %v", err)
				return
			}
			results[i] = rows
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if !reflect.DeepEqual(results[i], refs) {
			t.Fatalf("goroutine %d 的结果与基准不一致", i)
		}
	}
}

// TestSelfCheck 钉住自检入口：对内置操作序列核验四条不变量。
func TestSelfCheck(t *testing.T) {
	a := New()
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	// SelfCheck 并发调用也必须安全。
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.SelfCheck(); err != nil {
				t.Errorf("并发 SelfCheck: %v", err)
			}
		}()
	}
	wg.Wait()
}
