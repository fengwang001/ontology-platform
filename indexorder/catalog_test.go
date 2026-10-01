package indexorder

import (
	"errors"
	"fmt"
	"log"
	"os"
	"testing"
)

var testLogger = log.New(os.Stdout, "", log.LstdFlags)

func eqSet(cols ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		m[c] = struct{}{}
	}
	return m
}

func logChoose(t *testing.T, cat *Catalog, eq map[string]struct{}, order []ColumnItem, want Choice) {
	t.Helper()
	got, err := cat.Choose(eq, order)
	testLogger.Printf("CHOOSE input eq=%v order=%v => output=%+v err=%v | 依据: need=%+v, 正向优先/列数/名字 => %+v",
		eq, order, got, err, normalizeOrder(eq, order), want)
	if err != nil || got != want {
		t.Fatalf("Choose(eq=%v, order=%v) = %+v, %v; want %+v", eq, order, got, err, want)
	}
}

func reg(t *testing.T, cat *Catalog, idx Index) {
	t.Helper()
	if err := cat.Register(idx); err != nil {
		t.Fatalf("Register(%v): %v", idx.Name, err)
	}
	testLogger.Printf("REGISTER input %+v => ok", idx)
}

// 索引 (a ASC NULLS LAST, b DESC NULLS FIRST)：同序 ORDER BY 正向满足；
// 全部取反 ORDER BY 反向满足；只取反方向不取反空值位置不满足。
func TestDirectionAndNulls(t *testing.T) {
	cat := NewCatalog()
	idx := Index{Name: "i_ab", Column: []ColumnItem{
		{Column: "a", Dir: ASC, Nulls: NullsLast},
		{Column: "b", Dir: DESC, Nulls: NullsFirst},
	}}
	reg(t, cat, idx)

	same := []ColumnItem{
		{Column: "a", Dir: ASC, Nulls: NullsLast},
		{Column: "b", Dir: DESC, Nulls: NullsFirst},
	}
	logChoose(t, cat, nil, same, Choice{IndexName: "i_ab", Scan: ScanForward})

	inverted := []ColumnItem{
		{Column: "a", Dir: DESC, Nulls: NullsFirst},
		{Column: "b", Dir: ASC, Nulls: NullsLast},
	}
	logChoose(t, cat, nil, inverted, Choice{IndexName: "i_ab", Scan: ScanBackward})

	dirOnly := []ColumnItem{
		{Column: "a", Dir: DESC, Nulls: NullsLast},
		{Column: "b", Dir: ASC, Nulls: NullsFirst},
	}
	if _, err := cat.Choose(nil, dirOnly); !errors.Is(err, ErrNoMatchingIndex) {
		t.Fatalf("dir-only-inverted order: got %v, want ErrNoMatchingIndex", err)
	}
	testLogger.Printf("CHOOSE order=%v => err=%v | 依据: 空值位置未同步取反, 正反向均不匹配", dirOnly, ErrNoMatchingIndex)
}

// eq 列夹在索引中间被跳过：索引 (a,b,c)，eq={b}，order=(a,c) 满足。
func TestEqColumnSkippedInMiddle(t *testing.T) {
	cat := NewCatalog()
	reg(t, cat, Index{Name: "i_abc", Column: []ColumnItem{
		{Column: "a", Dir: ASC, Nulls: NullsLast},
		{Column: "b", Dir: ASC, Nulls: NullsLast},
		{Column: "c", Dir: ASC, Nulls: NullsLast},
	}})
	order := []ColumnItem{
		{Column: "a", Dir: ASC, Nulls: NullsLast},
		{Column: "c", Dir: ASC, Nulls: NullsLast},
	}
	logChoose(t, cat, eqSet("b"), order, Choice{IndexName: "i_abc", Scan: ScanForward})
}

// order 中属于 eq 的项被丢弃；重复列只保留第一次出现的项。
func TestNormalizeOrder(t *testing.T) {
	cat := NewCatalog()
	reg(t, cat, Index{Name: "i_ac", Column: []ColumnItem{
		{Column: "a", Dir: ASC, Nulls: NullsLast},
		{Column: "c", Dir: DESC, Nulls: NullsFirst},
	}})

	// eq={b}：order 中的 b 项被丢弃。
	withEq := []ColumnItem{
		{Column: "b", Dir: DESC, Nulls: NullsFirst},
		{Column: "a", Dir: ASC, Nulls: NullsLast},
		{Column: "c", Dir: DESC, Nulls: NullsFirst},
	}
	logChoose(t, cat, eqSet("b"), withEq, Choice{IndexName: "i_ac", Scan: ScanForward})

	// 重复列只保留第一次：a 第一次为 ASC NULLS LAST，第二次的取反写法被忽略。
	dup := []ColumnItem{
		{Column: "a", Dir: ASC, Nulls: NullsLast},
		{Column: "a", Dir: DESC, Nulls: NullsFirst},
		{Column: "c", Dir: DESC, Nulls: NullsFirst},
	}
	logChoose(t, cat, nil, dup, Choice{IndexName: "i_ac", Scan: ScanForward})
}

// need 为空时任何索引都以正向满足。
func TestEmptyNeed(t *testing.T) {
	cat := NewCatalog()
	reg(t, cat, Index{Name: "zzz", Column: []ColumnItem{
		{Column: "x", Dir: DESC, Nulls: NullsFirst},
	}})
	reg(t, cat, Index{Name: "aaa", Column: []ColumnItem{
		{Column: "y", Dir: ASC, Nulls: NullsLast},
		{Column: "z", Dir: ASC, Nulls: NullsLast},
	}})
	// order 全部被 eq 丢弃 -> need 空 -> 正向优先 + 列数少 + 名字。
	order := []ColumnItem{{Column: "y", Dir: DESC, Nulls: NullsFirst}}
	logChoose(t, cat, eqSet("y"), order, Choice{IndexName: "zzz", Scan: ScanForward})

	// 完全没有 order 项同样 need 为空。
	logChoose(t, cat, eqSet("q"), nil, Choice{IndexName: "zzz", Scan: ScanForward})
}

// 正向索引优先于更短的反向索引。
func TestForwardBeatsShorterBackward(t *testing.T) {
	cat := NewCatalog()
	reg(t, cat, Index{Name: "long_fwd", Column: []ColumnItem{
		{Column: "a", Dir: ASC, Nulls: NullsLast},
		{Column: "b", Dir: ASC, Nulls: NullsLast},
		{Column: "c", Dir: ASC, Nulls: NullsLast},
	}})
	reg(t, cat, Index{Name: "short_back", Column: []ColumnItem{
		{Column: "a", Dir: DESC, Nulls: NullsFirst},
		{Column: "b", Dir: DESC, Nulls: NullsFirst},
	}})
	order := []ColumnItem{
		{Column: "a", Dir: ASC, Nulls: NullsLast},
		{Column: "b", Dir: ASC, Nulls: NullsLast},
	}
	logChoose(t, cat, nil, order, Choice{IndexName: "long_fwd", Scan: ScanForward})
}

// 列项数并列时按名字字节序取小。
func TestTieByName(t *testing.T) {
	cat := NewCatalog()
	reg(t, cat, Index{Name: "idx_b", Column: []ColumnItem{
		{Column: "a", Dir: ASC, Nulls: NullsLast},
	}})
	reg(t, cat, Index{Name: "idx_a", Column: []ColumnItem{
		{Column: "a", Dir: ASC, Nulls: NullsLast},
	}})
	logChoose(t, cat, nil,
		[]ColumnItem{{Column: "a", Dir: ASC, Nulls: NullsLast}},
		Choice{IndexName: "idx_a", Scan: ScanForward})
}

func TestRegisterErrorOrdering(t *testing.T) {
	cat := NewCatalog()
	cases := []struct {
		name string
		idx  Index
		want error
	}{
		{"empty name", Index{Name: "", Column: []ColumnItem{{Column: "a"}}}, ErrEmptyName},
		{"empty columns", Index{Name: "n"}, ErrEmptyColumns},
		{"empty column name", Index{Name: "n", Column: []ColumnItem{{Column: ""}}}, ErrEmptyColumnName},
		{"bad dir", Index{Name: "n", Column: []ColumnItem{{Column: "a", Dir: "SIDEWAYS"}}}, ErrInvalidDirection},
		{"bad nulls", Index{Name: "n", Column: []ColumnItem{{Column: "a", Dir: ASC, Nulls: "MIDDLE"}}}, ErrInvalidNullsOrder},
		{"dup column", Index{Name: "n", Column: []ColumnItem{
			{Column: "a", Dir: ASC, Nulls: NullsLast},
			{Column: "a", Dir: ASC, Nulls: NullsLast},
		}}, ErrDuplicateColumn},
	}
	for _, tc := range cases {
		if err := cat.Register(tc.idx); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, err, tc.want)
		}
		testLogger.Printf("REGISTER input=%+v => err=%v", tc.idx, tc.want)
	}
	// 名字已存在先于列非法被报告。
	if err := cat.Register(Index{Name: "n", Column: []ColumnItem{{Column: "a", Dir: ASC, Nulls: NullsLast}}}); err != nil {
		t.Fatal(err)
	}
	err := cat.Register(Index{Name: "n"})
	if !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("existing name: got %v, want ErrDuplicateName", err)
	}
	testLogger.Printf("REGISTER duplicate name with bad body => err=%v", err)

	if len(cat.indexes) != 1 {
		t.Fatalf("failed registers mutated state: %d indexes", len(cat.indexes))
	}
}

func TestDropAndChooseErrors(t *testing.T) {
	cat := NewCatalog()
	if err := cat.Drop("ghost"); !errors.Is(err, ErrIndexNotFound) {
		t.Fatalf("drop missing: %v", err)
	}

	badChoose := []struct {
		eq    map[string]struct{}
		order []ColumnItem
		want  error
	}{
		{eqSet(""), nil, ErrEqEmptyColumnName},
		{nil, []ColumnItem{{Column: ""}}, ErrOrderEmptyColumnName},
		{nil, []ColumnItem{{Column: "a", Dir: "X"}}, ErrOrderInvalidDirection},
		{nil, []ColumnItem{{Column: "a", Dir: ASC, Nulls: "X"}}, ErrOrderInvalidNulls},
	}
	for _, tc := range badChoose {
		if _, err := cat.Choose(tc.eq, tc.order); !errors.Is(err, tc.want) {
			t.Fatalf("Choose(eq=%v, order=%v): got %v, want %v", tc.eq, tc.order, err, tc.want)
		}
		testLogger.Printf("CHOOSE invalid eq=%v order=%v => err=%v", tc.eq, tc.order, tc.want)
	}

	reg(t, cat, Index{Name: "i", Column: []ColumnItem{{Column: "a", Dir: ASC, Nulls: NullsLast}}})
	if _, err := cat.Choose(nil, []ColumnItem{{Column: "z", Dir: ASC, Nulls: NullsLast}}); !errors.Is(err, ErrNoMatchingIndex) {
		t.Fatalf("want no match, got %v", err)
	}
	// 非法输入先于“无索引满足”。
	if _, err := cat.Choose(nil, []ColumnItem{{Column: "z", Dir: "BAD"}}); !errors.Is(err, ErrOrderInvalidDirection) {
		t.Fatalf("invalid input must precede no-match, got %v", err)
	}
	if err := cat.Drop("i"); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.Choose(nil, []ColumnItem{{Column: "a", Dir: ASC, Nulls: NullsLast}}); !errors.Is(err, ErrNoMatchingIndex) {
		t.Fatalf("dropped index still chosen: %v", err)
	}
	testLogger.Printf("DROP i then CHOOSE => err=%v (已 Drop 不再被选)", ErrNoMatchingIndex)
}

func TestChoiceIndependentOfRegistrationOrder(t *testing.T) {
	indexes := []Index{
		{Name: "same1", Column: []ColumnItem{{Column: "a", Dir: ASC, Nulls: NullsLast}}},
		{Name: "same2", Column: []ColumnItem{{Column: "a", Dir: ASC, Nulls: NullsLast}}},
	}
	order := []ColumnItem{{Column: "a", Dir: ASC, Nulls: NullsLast}}
	c1 := NewCatalog()
	reg(t, c1, indexes[0])
	reg(t, c1, indexes[1])
	c2 := NewCatalog()
	reg(t, c2, indexes[1])
	reg(t, c2, indexes[0])
	ch1, err1 := c1.Choose(nil, order)
	ch2, err2 := c2.Choose(nil, order)
	if err1 != nil || err2 != nil || ch1 != ch2 {
		t.Fatalf("registration order changed choice: %+v vs %+v", ch1, ch2)
	}
	testLogger.Printf("REPLAY order-independence => both %+v", ch1)
}

func ExampleCatalog() {
	cat := NewCatalog()
	_ = cat.Register(Index{Name: "idx", Column: []ColumnItem{
		{Column: "a", Dir: ASC, Nulls: NullsLast},
	}})
	ch, err := cat.Choose(nil, []ColumnItem{{Column: "a", Dir: ASC, Nulls: NullsLast}})
	fmt.Println(ch, err)
	// Output: {idx FORWARD} <nil>
}
