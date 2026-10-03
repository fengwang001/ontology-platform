package adapter

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, h int) *Registry {
	t.Helper()
	r, err := New(h)
	if err != nil {
		t.Fatalf("New(%d) 返回错误 %v", h, err)
	}
	return r
}

func TestNewValidation(t *testing.T) {
	for _, h := range []int{0, -1, 1001} {
		if _, err := New(h); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%d) err=%v，期望非法参数", h, err)
		}
		t.Logf("New 边界 h=%d 正确拒绝", h)
	}
	for _, h := range []int{1, 1000} {
		if r, err := New(h); err != nil || r.H() != h {
			t.Fatalf("New(%d) = %v,%v", h, r, err)
		}
		t.Logf("New 边界 h=%d 正确接受", h)
	}
}

func TestMutationsAndRevision(t *testing.T) {
	r := mustNew(t, 4)
	if got := r.Revision(); got != 0 {
		t.Fatalf("初始版本号=%d，期望0", got)
	}
	cases := []struct {
		name string
		fn   func() error
		want error
		rev  int
		note string
	}{
		{"登记步1", func() error { return r.SetAdapter(1, false, true) }, nil, 1, "成功+1"},
		{"重复登记步1覆盖", func() error { return r.SetAdapter(1, true, true) }, nil, 2, "覆盖也算成功变更"},
		{"登记步3", func() error { return r.SetAdapter(3, true, false) }, nil, 3, "步2保持缺失"},
		{"登记步越界0", func() error { return r.SetAdapter(0, false, false) }, ErrInvalidArgument, 3, "拒绝不加版本号"},
		{"登记步越界H", func() error { return r.SetAdapter(4, false, false) }, ErrInvalidArgument, 3, "v=H 非法"},
		{"下线3@100", func() error { return r.Sunset(3, 100) }, nil, 4, "成功+1"},
		{"下线越界版本4", func() error { return r.Sunset(4, 100) }, ErrInvalidArgument, 4, "v=H 不能下线"},
		{"下线时间越界负", func() error { return r.Sunset(1, -1) }, ErrInvalidArgument, 4, "t<0"},
		{"下线时间越界大", func() error { return r.Sunset(1, 1_000_000_000_000_001) }, ErrInvalidArgument, 4, "t>1e15"},
		{"下线时间边界0", func() error { return r.Sunset(1, 0) }, nil, 5, "t=0 合法"},
		{"预览4 beta", func() error { return r.Preview(4, "beta") }, nil, 6, "v=H 可预览"},
		{"预览空作用域", func() error { return r.Preview(4, "") }, ErrInvalidArgument, 6, "scope 非空"},
		{"预览越界版本", func() error { return r.Preview(5, "x") }, ErrInvalidArgument, 6, "v>H"},
		{"预览覆盖作用域", func() error { return r.Preview(4, "gamma") }, nil, 7, "覆盖作用域"},
		{"删除未登记步2", func() error { return r.RemoveAdapter(2) }, ErrNotFound, 7, "对象不存在，不加版本号"},
		{"删除越界步", func() error { return r.RemoveAdapter(9) }, ErrInvalidArgument, 7, "参数非法优先"},
		{"删除已登记步3", func() error { return r.RemoveAdapter(3) }, nil, 8, "成功+1"},
		{"再次删除步3", func() error { return r.RemoveAdapter(3) }, ErrNotFound, 8, "已不存在"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.fn()
			if !errors.Is(err, c.want) {
				t.Fatalf("%s err=%v，期望 %v，依据：%s", c.name, err, c.want, c.note)
			}
			if got := r.Revision(); got != c.rev {
				t.Fatalf("%s 后版本号=%d，期望 %d，依据：%s", c.name, got, c.rev, c.note)
			}
			t.Logf("操作=%s 输出err=%v 版本号=%d 判定依据=%s", c.name, err, c.rev, c.note)
		})
	}

	snap := r.SnapshotAt()
	if snap.Rev != 8 {
		t.Fatalf("快照版本号=%d，期望8", snap.Rev)
	}
	if s, ok := snap.Steps[1]; !ok || !s.ReqLossy || !s.RespLossy {
		t.Fatalf("步1应为覆盖后的(真,真)，实际 %+v ok=%v", s, ok)
	}
	if _, ok := snap.Steps[3]; ok {
		t.Fatalf("步3应已删除")
	}
	if snap.Sunset[1] != 0 || snap.Sunset[3] != 100 {
		t.Fatalf("下线表异常：%+v", snap.Sunset)
	}
	if snap.Preview[4] != "gamma" {
		t.Fatalf("版本4预览作用域应为 gamma，实际 %q", snap.Preview[4])
	}

	snap.Steps[1] = Step{}
	delete(snap.Sunset, 3)
	fresh := r.SnapshotAt()
	if fresh.Steps[1].ReqLossy != true || fresh.Sunset[3] != 100 {
		t.Fatalf("快照必须是深拷贝，修改快照污染了注册表")
	}
	t.Logf("快照深拷贝校验通过")
}

func TestHeadVersionOne(t *testing.T) {
	r := mustNew(t, 1)
	if err := r.SetAdapter(1, false, false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("H=1 时不存在适配步，err=%v", err)
	}
	if err := r.Preview(1, "x"); err != nil {
		t.Fatalf("H=1 时版本1可预览：%v", err)
	}
	if r.Revision() != 1 {
		t.Fatalf("版本号=%d，期望1", r.Revision())
	}
	t.Logf("H=1 边界行为正确，版本号=%d", r.Revision())
}
