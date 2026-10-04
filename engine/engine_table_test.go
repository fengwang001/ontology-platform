package engine

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

type tableCase struct {
	name string
	run  func(t *testing.T)
}

func TestTableDriven(t *testing.T) {
	cases := []tableCase{
		{"Get与Search可见性差", func(t *testing.T) {
			e := New(Async)
			mustIdx(t, e, "x", bb("old"), -1, 1)
			e.Refresh()
			mustIdx(t, e, "x", bb("new"), -1, 2)
			mustIdx(t, e, "y", bb("ny"), -1, 3)
			d, err := e.Get("x")
			if err != nil || string(d.Body) != "new" {
				t.Fatalf("Get(x)=%q err=%v, 期望 new", d.Body, err)
			}
			if _, err := e.Get("y"); err != nil {
				t.Fatalf("未刷新的 y 实时 Get 应可见: %v", err)
			}
			docs, _ := e.Search()
			if len(docs) != 1 || docs[0].ID != "x" || string(docs[0].Body) != "old" {
				t.Fatalf("Search 应只见已刷新的 old x, 得 %+v", docs)
			}
			e.Refresh()
			docs, _ = e.Search()
			d2, _ := e.Get("x")
			if !bytes.Equal(docs[0].Body, d2.Body) || docs[0].Seq != d2.Seq {
				t.Fatalf("Refresh 后 Get/Search 不一致")
			}
		}},
		{"刷新过但未落盘崩溃后消失", func(t *testing.T) {
			e := New(Async)
			mustIdx(t, e, "a", bb("1"), -1, 1)
			e.Flush()
			mustIdx(t, e, "b", bb("2"), -1, 2)
			e.Refresh()
			if ids := searchIDs(e); fmt.Sprint(ids) != "[a b]" {
				t.Fatalf("Refresh 后应见 a b, 得 %v", ids)
			}
			mustIdx(t, e, "c", bb("3"), -1, 3)
			e.Crash()
			n, _ := e.Recover()
			if n != 0 {
				t.Fatalf("重放=%d, 期望 0", n)
			}
			if ids := searchIDs(e); fmt.Sprint(ids) != "[a]" {
				t.Fatalf("恢复后 Search=%v, 期望 [a]", ids)
			}
		}},
		{"切到Request立即落盘", func(t *testing.T) {
			e := New(Async)
			mustIdx(t, e, "a", bb("1"), -1, 1)
			mustIdx(t, e, "b", bb("2"), -1, 2)
			if e.Synced() != 0 {
				t.Fatalf("Async synced=%d, 期望 0", e.Synced())
			}
			if err := e.SetDurability(Request); err != nil || e.Synced() != 2 {
				t.Fatalf("切 Request 后 synced=%d err=%v", e.Synced(), err)
			}
			mustIdx(t, e, "c", bb("3"), -1, 3)
			if e.Synced() != 3 {
				t.Fatalf("Request 写后 synced=%d, 期望 3", e.Synced())
			}
			e.Crash()
			n, _ := e.Recover()
			if n != 3 || e.replayed != 3 {
				t.Fatalf("重放 n=%d replayed=%d, 期望 3", n, e.replayed)
			}
		}},
		{"Flush后重放数为0", func(t *testing.T) {
			e := New(Async)
			for i := int64(1); i <= 4; i++ {
				mustIdx(t, e, fmt.Sprintf("id%d", i), bb("v"), -1, i)
			}
			e.Sync()
			e.Flush()
			if e.Uncommitted() != 0 || e.Committed() != 4 {
				t.Fatalf("Flush 后未提交=%d committed=%d", e.Uncommitted(), e.Committed())
			}
			e.Crash()
			n, _ := e.Recover()
			if n != 0 {
				t.Fatalf("Flush 后重放=%d, 期望 0", n)
			}
			if len(searchIDs(e)) != 4 {
				t.Fatalf("恢复后应从提交点装回 4 条")
			}
		}},
		{"连续两次崩溃恢复幂等", func(t *testing.T) {
			e := New(Async)
			mustIdx(t, e, "a", bb("1"), -1, 1)
			mustIdx(t, e, "b", bb("2"), -1, 2)
			e.Flush()
			mustIdx(t, e, "c", bb("3"), -1, 3)
			mustDel(t, e, "a", 4)
			e.Sync()
			mustIdx(t, e, "d", bb("4"), -1, 5)
			e.Crash()
			n1, _ := e.Recover()
			ids1 := searchIDs(e)
			max1 := e.MaxSeq()
			e.Crash()
			n2, _ := e.Recover()
			ids2 := searchIDs(e)
			if n1 != 2 || n2 != 2 {
				t.Fatalf("重放条数 n1=%d n2=%d, 期望都为 2", n1, n2)
			}
			want := "[b c]"
			if fmt.Sprint(ids1) != want || fmt.Sprint(ids2) != want {
				t.Fatalf("ids1=%v ids2=%v, 期望 %s", ids1, ids2, want)
			}
			if max1 != 4 || e.MaxSeq() != 4 {
				t.Fatalf("maxSeq 一次=%d 二次=%d, 期望都为 4", max1, e.MaxSeq())
			}
		}},
		{"序号重新分配", func(t *testing.T) {
			e := New(Async)
			mustIdx(t, e, "a", bb("1"), -1, 1)
			e.Flush()
			mustIdx(t, e, "b", bb("2"), -1, 2)
			mustIdx(t, e, "c", bb("3"), -1, 3)
			e.Sync()
			mustIdx(t, e, "d", bb("4"), -1, 4)
			e.Crash()
			n, _ := e.Recover()
			if n != 2 || e.MaxSeq() != 3 {
				t.Fatalf("重放=%d maxSeq=%d, 期望 2,3", n, e.MaxSeq())
			}
			mustIdx(t, e, "d", bb("4"), -1, 4)
			mustIdx(t, e, "e", bb("5"), -1, 5)
		}},
		{"拒绝操作不占序号", func(t *testing.T) {
			e := New(Async)
			if _, err := e.Index("", bb("x"), -1); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("空 id err=%v", err)
			}
			if _, err := e.Index("a", make([]byte, 65537), -1); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("超长 body err=%v", err)
			}
			if _, err := e.Index("a", bb("x"), 0); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("ifSeq=0 err=%v", err)
			}
			if _, err := e.Delete("ghost"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("删除不存在 err=%v", err)
			}
			if _, err := e.Index("ghost", bb("x"), 1); !errors.Is(err, ErrConflict) {
				t.Fatalf("对不存在文档带 ifSeq err=%v", err)
			}
			if e.MaxSeq() != 0 || e.Uncommitted() != 0 {
				t.Fatalf("被拒绝操作占用了序号: maxSeq=%d len=%d", e.MaxSeq(), e.Uncommitted())
			}
			mustIdx(t, e, "ghost", bb("x"), -1, 1)
			if _, err := e.Delete("ghost"); err != nil {
				t.Fatalf("删除被拒后 id 应仍可用: %v", err)
			}
		}},
		{"崩溃态拒绝次序", func(t *testing.T) {
			e := New(Async)
			mustIdx(t, e, "a", bb("1"), -1, 1)
			e.Crash()
			if _, err := e.Index("", bb("x"), -1); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("崩溃态参数非法 err=%v", err)
			}
			if _, err := e.Index("b", bb("x"), -1); !errors.Is(err, ErrNotRecovered) {
				t.Fatalf("崩溃态 Index err=%v", err)
			}
			if _, err := e.Delete("a"); !errors.Is(err, ErrNotRecovered) {
				t.Fatalf("崩溃态 Delete err=%v", err)
			}
			if _, err := e.Get("a"); !errors.Is(err, ErrNotRecovered) {
				t.Fatalf("崩溃态 Get err=%v", err)
			}
			if _, err := e.Search(); !errors.Is(err, ErrNotRecovered) {
				t.Fatalf("崩溃态 Search err=%v", err)
			}
			for _, fn := range []func() error{e.Refresh, e.Sync, e.Flush} {
				if err := fn(); !errors.Is(err, ErrNotRecovered) {
					t.Fatalf("崩溃态管理操作 err=%v", err)
				}
			}
			if err := e.SetDurability(Durability(9)); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("崩溃态非法 durability 应先判参数, err=%v", err)
			}
			if err := e.SetDurability(Async); !errors.Is(err, ErrNotRecovered) {
				t.Fatalf("崩溃态 SetDurability err=%v", err)
			}
			if err := e.Crash(); !errors.Is(err, ErrNotRecovered) {
				t.Fatalf("崩溃态再 Crash err=%v", err)
			}
			if _, err := e.Recover(); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Recover(); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("非崩溃态 Recover err=%v", err)
			}
		}},
		{"条件写针对实时状态", func(t *testing.T) {
			e := New(Async)
			mustIdx(t, e, "a", bb("1"), -1, 1)
			e.Refresh()
			mustDel(t, e, "a", 2)
			if _, err := e.Index("a", bb("x"), 1); !errors.Is(err, ErrConflict) {
				t.Fatalf("对实时删除但搜索仍在的 id 带 ifSeq err=%v", err)
			}
			mustIdx(t, e, "a", bb("2"), -1, 3)
			mustIdx(t, e, "a", bb("3"), 3, 4)
			e.Refresh()
			if d, _ := e.Get("a"); string(d.Body) != "3" || d.Seq != 4 {
				t.Fatalf("最终 Get(a)=%q@%d", d.Body, d.Seq)
			}
		}},
		{"Get触碰记录不超过2", func(t *testing.T) {
			e := New(Async)
			mustIdx(t, e, "a", bb("1"), -1, 1)
			e.Get("a")
			if e.getTouches != 1 {
				t.Fatalf("版本表命中 touches=%d, 期望 1", e.getTouches)
			}
			e.Refresh()
			e.Get("a")
			if e.getTouches != 2 {
				t.Fatalf("版本表未命中、视图命中 touches=%d, 期望 2", e.getTouches)
			}
			e.Get("missing")
			if e.getTouches != 2 {
				t.Fatalf("两处都未命中 touches=%d, 期望 2", e.getTouches)
			}
		}},
		{"字节序与返回拷贝", func(t *testing.T) {
			e := New(Async)
			mustIdx(t, e, "b", bb("1"), -1, 1)
			mustIdx(t, e, "a", bb("2"), -1, 2)
			mustIdx(t, e, "A", bb("3"), -1, 3)
			e.Refresh()
			if ids := searchIDs(e); fmt.Sprint(ids) != "[A a b]" {
				t.Fatalf("字节序=%v, 期望 [A a b]", ids)
			}
			docs, _ := e.Search()
			docs[0].Body[0] = 'Z'
			d, _ := e.Get("A")
			if d.Body[0] != '3' {
				t.Fatalf("返回 body 非副本, 内部数据被改: %q", d.Body)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.run(t) })
	}
}
