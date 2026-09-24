package api_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/sch"
)

func sixVer(t *testing.T) *api.Service {
	t.Helper()
	s := api.New(api.Column{Name: "x", Typ: sch.Int}, api.Column{Name: "y", Typ: sch.Str}, api.Column{Name: "m", Typ: sch.Str})
	for _, e := range []error{
		s.AddColumn("z", "str", true),
		s.ChangeType("y", "int"),
		s.ChangeType("x", "str"),
		s.DropColumn("m"),
		s.AddColumn("w", "int", false),
	} {
		if e != nil {
			t.Fatal(e)
		}
	}
	return s
}

// 第三节六行表：逐列映射或拒收。
func TestSixEvents(t *testing.T) {
	s := sixVer(t)
	cases := []struct {
		ver  int
		vals []any
		want []any
		err  error
	}{
		{6, []any{"1", int64(2), "a", int64(3)}, []any{"1", int64(2), "a", int64(3)}, nil},
		{2, []any{int64(5), "6", "M", "b"}, []any{"5", int64(6), "b", int64(0)}, nil},
		{2, []any{int64(5), "abc", "M", "b"}, nil, sch.ErrBadValue},
		{4, []any{"9", int64(10), "M", "d"}, []any{"9", int64(10), "d", int64(0)}, nil},
		{1, []any{int64(11), "12", "M"}, nil, sch.ErrMissingColumn},
		{3, []any{int64(13), int64(14), "M", "e"}, []any{"13", int64(14), "e", int64(0)}, nil},
	}
	for i, c := range cases {
		got, err := s.Map(c.ver, c.vals)
		if !errors.Is(err, c.err) || (err == nil && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("事件%d: got %v,%v want %v,%v", i+1, got, err, c.want, c.err)
		}
	}
}

// 不变量2：按名对齐——已删列 m 静默丢弃，z/w 不得错位。
func TestNameAlignment(t *testing.T) {
	got, err := sixVer(t).Map(2, []any{int64(5), "6", "M", "b"})
	if err != nil || got[2] != "b" || got[3] != int64(0) {
		t.Fatalf("按名对齐失败: %v,%v", got, err)
	}
}

// 不变量3：同版本同值反复 Map 结果完全确定。
func TestDeterministic(t *testing.T) {
	s := sixVer(t)
	first, _ := s.Map(2, []any{int64(5), "6", "M", "b"})
	for i := 0; i < 16; i++ {
		if got, _ := s.Map(2, []any{int64(5), "6", "M", "b"}); !reflect.DeepEqual(got, first) {
			t.Fatal("结果漂移")
		}
	}
}

// 不变量4 + 故障注入：四类错误互不相同，被拒后状态不变且可继续用。
func TestRejectNoSideEffect(t *testing.T) {
	s := sixVer(t)
	before := fmt.Sprint(s.Active())
	reps := map[error]error{
		s.AddColumn("q", "float", false): sch.ErrBadType,
		s.AddColumn("", "int", false):    sch.ErrEmptyName,
		s.AddColumn("x", "int", false):   sch.ErrDuplicateColumn,
		s.ChangeType("no", "int"):        sch.ErrNoSuchColumn,
		s.DropColumn("no"):               sch.ErrNoSuchColumn,
	}
	for got, want := range reps {
		if !errors.Is(got, want) {
			t.Fatalf("%v 应为 %v", got, want)
		}
	}
	sents := []error{sch.ErrBadType, sch.ErrEmptyName, sch.ErrDuplicateColumn, sch.ErrNoSuchColumn, sch.ErrVersionUnregistered, sch.ErrMissingColumn, sch.ErrBadValue}
	for i := range sents {
		for j := i + 1; j < len(sents); j++ {
			if errors.Is(sents[i], sents[j]) {
				t.Fatalf("哨兵不互异: %v vs %v", sents[i], sents[j])
			}
		}
	}
	if _, err := s.Map(99, nil); !errors.Is(err, sch.ErrVersionUnregistered) {
		t.Fatal(err)
	}
	if fmt.Sprint(s.Active()) != before {
		t.Fatal("被拒操作改变了状态")
	}
	_, err6 := s.Map(6, []any{"1", int64(2), "a", int64(3)})
	if err6 != nil || s.SelfCheck() != nil {
		t.Fatal("拒绝后不可用或自检失败")
	}
}

// 并发：一个 goroutine 反复 AddColumn，N 个并发 Map 同一旧事件，结果严格对应某一完整版本，不用 sleep。
func TestConcurrentMap(t *testing.T) {
	s := api.New(api.Column{Name: "a", Typ: sch.Int})
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			s.AddColumn(fmt.Sprintf("c%d", i), []string{"int", "str"}[i%2], false)
		}
		stop.Store(true)
	}()
	work := func(f func()) {
		defer wg.Done()
		for !stop.Load() {
			f()
		}
	}
	mapper := func() {
		got, err := s.Map(1, []any{int64(7)})
		if err != nil || got[0] != int64(7) {
			t.Errorf("Map: %v %v", got, err)
			return
		}
		for j := 1; j < len(got); j++ {
			if want := []any{int64(0), ""}[(j-1)%2]; got[j] != want {
				t.Errorf("混合版本: %v", got)
				return
			}
		}
	}
	wg.Add(9)
	for g := 0; g < 8; g++ {
		go work(mapper)
	}
	go work(func() { _ = s.SelfCheck(); _ = s.Active() })
	wg.Wait()
}
