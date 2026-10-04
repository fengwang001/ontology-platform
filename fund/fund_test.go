package fund

import (
	"errors"
	"testing"
)

func TestLedgerBasics(t *testing.T) {
	l := New()
	if err := l.AddPerson(""); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("空名非法, got %v", err)
	}
	if err := l.AddPerson("p1"); err != nil {
		t.Fatal(err)
	}
	if err := l.AddPerson("p1"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("重复登记非法, got %v", err)
	}
	if !l.HasPerson("p1") || l.HasPerson("p2") {
		t.Fatal("HasPerson 错误")
	}
	if _, err := l.Use("p2", 1); !errors.Is(err, ErrPersonUnknown) {
		t.Fatalf("未知参保人, got %v", err)
	}
	if _, err := l.Use("p1", 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("年度0非法, got %v", err)
	}
}

func TestCommitAndRestore(t *testing.T) {
	l := New()
	if err := l.AddPerson("p"); err != nil {
		t.Fatal(err)
	}

	// 第一笔
	b1, err := l.Use("p", 2024)
	if err != nil || b1 != (Acc{}) {
		t.Fatalf("新年度应为零, got %+v err=%v", b1, err)
	}
	d1 := Acc{Du: 100, X: 0, F: 0, P: 100, Q: 0}
	if err := l.Commit("p", 2024, d1); err != nil {
		t.Fatal(err)
	}
	s1, _ := l.Snapshot("p", 2024)
	if s1 != d1 {
		t.Fatalf("第一笔后 = %+v, want %+v", s1, d1)
	}

	// 第二笔（同年）
	b2, err := l.Use("p", 2024)
	if err != nil {
		t.Fatal(err)
	}
	if b2 != d1 {
		t.Fatalf("Use 应返回当前值 %+v, got %+v", d1, b2)
	}
	d2 := Acc{Du: 50, X: 200, F: 140, P: 110, Q: 10}
	if err := l.Commit("p", 2024, d2); err != nil {
		t.Fatal(err)
	}
	s2, _ := l.Snapshot("p", 2024)
	want2 := Acc{Du: 150, X: 200, F: 140, P: 210, Q: 10}
	if s2 != want2 {
		t.Fatalf("第二笔后 = %+v, want %+v", s2, want2)
	}

	// 冲正第二笔：恢复到第二笔前
	got, err := l.Restore("p", 2024, b2)
	if err != nil || got != d1 {
		t.Fatalf("冲正第二笔应回 %+v, got %+v err=%v", d1, got, err)
	}
	// 再冲正第一笔：恢复到零
	got, err = l.Restore("p", 2024, b1)
	if err != nil || got != (Acc{}) {
		t.Fatalf("冲正第一笔应回零, got %+v err=%v", got, err)
	}
	// 没有未冲正结算时再次恢复应失败
	if _, err := l.Restore("p", 2024, Acc{}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("无未冲正笔时 Restore 应失败, got %v", err)
	}
}

func TestYearMonotonic(t *testing.T) {
	l := New()
	_ = l.AddPerson("p")
	_, _ = l.Use("p", 2024)
	_ = l.Commit("p", 2024, Acc{Du: 1})

	if _, err := l.Use("p", 2023); !errors.Is(err, ErrYearClosed) {
		t.Fatalf("回到 2023 应 ErrYearClosed, got %v", err)
	}

	// 在 2025 结算一笔，再冲正 2024，再冲正 2025，之后 2025 可重新结算。
	b25, _ := l.Use("p", 2025)
	_ = l.Commit("p", 2025, Acc{Du: 2})
	if _, err := l.Use("p", 2024); !errors.Is(err, ErrYearClosed) {
		t.Fatalf("2025 已开后 2024 关闭, got %v", err)
	}
	// 先冲正 2025（栈顶），maxYear 回到 2024
	if _, err := l.Restore("p", 2025, b25); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Use("p", 2025); err != nil {
		t.Fatalf("冲正最新年度首笔后 2025 应可再开, got %v", err)
	}
}
