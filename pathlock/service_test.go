package pathlock

import (
	"reflect"
	"testing"
)

func mustLock(t *testing.T, s *Service, user, path string) Lock {
	t.Helper()
	l, err := s.Lock(user, path)
	if err != nil {
		t.Fatalf("Lock(%q, %q) 意外失败: %v", user, path, err)
	}
	return l
}

func mustErrCode(t *testing.T, err error, want ErrorCode) *Error {
	t.Helper()
	pe, ok := err.(*Error)
	if !ok {
		t.Fatalf("期望 *Error(%v)，得到 %v", want, err)
	}
	if pe.Code != want {
		t.Fatalf("期望错误码 %v，得到 %v (%v)", want, pe.Code, pe)
	}
	return pe
}

// TestLockConflictSelfVsOther 验证本人与他人冲突可区分，且都不改变原锁。
func TestLockConflictSelfVsOther(t *testing.T) {
	s := NewService()
	first := mustLock(t, s, "alice", "models/a.bin")

	_, err := s.Lock("alice", "models//a.bin/") // 规范化后相同
	pe := mustErrCode(t, err, ErrHeldBySelf)
	t.Logf("输入=alice 重复加锁 实际输出=%v 判定依据=期望 ErrHeldBySelf", pe.Code)

	_, err = s.Lock("bob", "models/a.bin")
	pe = mustErrCode(t, err, ErrHeldByOther)
	t.Logf("输入=bob 加锁同一路径 实际输出=%v holder=%q 判定依据=期望 ErrHeldByOther 且持有者为 alice", pe.Code, pe.Holder)
	if pe.Holder != "alice" {
		t.Errorf("期望持有者 alice，得到 %q", pe.Holder)
	}

	got, ok, _ := s.Lookup("models/a.bin")
	t.Logf("原锁核验 实际输出=%+v 判定依据=锁标识与创建时刻不变", got)
	if !ok || got.ID != first.ID || !got.CreatedAt.Equal(first.CreatedAt) || got.Owner != "alice" {
		t.Errorf("原锁被改变: %+v，原锁 %+v", got, first)
	}
}

// TestAncestorDescendantExclusion 验证祖先与后代的双向排他，以及本人不受限。
func TestAncestorDescendantExclusion(t *testing.T) {
	s := NewService()
	mustLock(t, s, "alice", "a/b")

	_, err := s.Lock("bob", "a")
	pe := mustErrCode(t, err, ErrAncestorOrDescendantConflict)
	t.Logf("输入=bob 锁祖先 a 实际输出=%v 冲突锁=%q 判定依据=后代 a/b 被 alice 持有", pe.Code, pe.Conflict.Path)
	if pe.Conflict.Path != "a/b" || pe.Holder != "alice" {
		t.Errorf("冲突锁应为 alice 的 a/b，得到 %+v", pe.Conflict)
	}

	_, err = s.Lock("bob", "a/b/c/d")
	pe = mustErrCode(t, err, ErrAncestorOrDescendantConflict)
	t.Logf("输入=bob 锁后代 a/b/c/d 实际输出=%v 冲突锁=%q 判定依据=祖先 a/b 被 alice 持有", pe.Code, pe.Conflict.Path)
	if pe.Conflict.Path != "a/b" {
		t.Errorf("冲突锁应为 a/b，得到 %+v", pe.Conflict)
	}

	if _, err := s.Lock("alice", "a"); err != nil {
		t.Errorf("本人锁祖先应放行，得到 %v", err)
	}
	if _, err := s.Lock("alice", "a/b/c/d"); err != nil {
		t.Errorf("本人锁后代应放行，得到 %v", err)
	}
	t.Logf("输入=alice 锁祖先与后代 实际输出=均成功 判定依据=本人不受祖先后代排他限制")
}

// TestConflictSelectionRule 验证多把冲突锁时选路径最短（字节长度）者，
// 长度相同取字节序最小者；祖先锁永远短于后代锁。
func TestConflictSelectionRule(t *testing.T) {
	s := NewService()
	mustLock(t, s, "alice", "a/bb/cc") // 长 7
	mustLock(t, s, "alice", "a/bb/d")  // 长 6，更短
	_, err := s.Lock("bob", "a/bb")
	pe := mustErrCode(t, err, ErrAncestorOrDescendantConflict)
	t.Logf("输入=bob 锁 a/bb 实际输出=冲突 %q 判定依据=后代中 a/bb/d(6) 短于 a/bb/cc(7)", pe.Conflict.Path)
	if pe.Conflict.Path != "a/bb/d" {
		t.Errorf("期望 a/bb/d，得到 %q", pe.Conflict.Path)
	}

	s2 := NewService()
	mustLock(t, s2, "alice", "x/ab")
	mustLock(t, s2, "alice", "x/aa")
	_, err = s2.Lock("bob", "x")
	pe = mustErrCode(t, err, ErrAncestorOrDescendantConflict)
	t.Logf("输入=bob 锁 x 实际输出=冲突 %q 判定依据=x/aa 与 x/ab 等长，取字节序最小 x/aa", pe.Conflict.Path)
	if pe.Conflict.Path != "x/aa" {
		t.Errorf("期望 x/aa，得到 %q", pe.Conflict.Path)
	}

	s3 := NewService()
	mustLock(t, s3, "alice", "m")
	mustLock(t, s3, "alice", "m/nn/o")
	_, err = s3.Lock("bob", "m/nn")
	pe = mustErrCode(t, err, ErrAncestorOrDescendantConflict)
	t.Logf("输入=bob 锁 m/nn 实际输出=冲突 %q 判定依据=祖先 m(1) 短于后代 m/nn/o(6)", pe.Conflict.Path)
	if pe.Conflict.Path != "m" {
		t.Errorf("期望 m，得到 %q", pe.Conflict.Path)
	}
}

// TestForceReleaseAndAudit 验证强制释放的权限、审计与锁标识的不可复用。
func TestForceReleaseAndAudit(t *testing.T) {
	s := NewService("root")
	victim := mustLock(t, s, "alice", "big/model.bin")

	err := s.Unlock("mallory", victim.ID, true)
	pe := mustErrCode(t, err, ErrPermissionDenied)
	t.Logf("输入=非管理员强制释放 实际输出=%v 判定依据=期望 ErrPermissionDenied", pe.Code)

	err = s.Unlock("bob", victim.ID, false)
	pe = mustErrCode(t, err, ErrNotOwner)
	t.Logf("输入=非持有者普通释放 实际输出=%v 判定依据=期望 ErrNotOwner", pe.Code)

	if err := s.Unlock("root", victim.ID, true); err != nil {
		t.Fatalf("管理员强制释放应成功: %v", err)
	}
	audit := s.AuditLog()
	t.Logf("输入=管理员强制释放 实际输出=审计 %+v 判定依据=强制释放记一条审计", audit)
	if len(audit) != 1 || audit[0].LockID != victim.ID || audit[0].Owner != "alice" ||
		audit[0].Admin != "root" || audit[0].Path != "big/model.bin" {
		t.Errorf("审计记录不正确: %+v", audit)
	}

	err = s.Unlock("root", victim.ID, true)
	pe = mustErrCode(t, err, ErrLockNotFound)
	t.Logf("输入=重复释放同一锁标识 实际输出=%v 判定依据=锁标识不可释放第二次", pe.Code)

	own := mustLock(t, s, "alice", "big/other.bin")
	if err := s.Unlock("alice", own.ID, false); err != nil {
		t.Fatalf("持有者普通释放应成功: %v", err)
	}
	t.Logf("输入=持有者普通释放 实际输出=审计数 %d 判定依据=普通释放不记审计", len(s.AuditLog()))
	if len(s.AuditLog()) != 1 {
		t.Errorf("普通释放不应记审计，审计数=%d", len(s.AuditLog()))
	}

	err = s.Unlock("alice", 99999, false)
	pe = mustErrCode(t, err, ErrLockNotFound)
	t.Logf("输入=释放不存在的锁 实际输出=%v 判定依据=期望 ErrLockNotFound", pe.Code)

	fresh := mustLock(t, s, "alice", "big/model.bin")
	t.Logf("锁标识核验 已释放=%d 新锁=%d 判定依据=锁标识全局唯一且单调不复用", victim.ID, fresh.ID)
	if fresh.ID <= victim.ID {
		t.Errorf("新锁标识 %d 应大于已释放的 %d", fresh.ID, victim.ID)
	}
}

// TestPushValidateAllOrNothing 验证批校验整体裁决、冲突列表排序去重、且零副作用。
func TestPushValidateAllOrNothing(t *testing.T) {
	s := NewService()
	mustLock(t, s, "alice", "a/1")
	mustLock(t, s, "alice", "a/2")
	mustLock(t, s, "carol", "z/deep/file")

	res, err := s.ValidatePush("bob", []string{"free/1", "a", "z/deep", "a/1"}, false)
	if err != nil {
		t.Fatalf("校验不应报错: %v", err)
	}
	var gotPaths []string
	var gotHolders []string
	for _, c := range res.Conflicts {
		gotPaths = append(gotPaths, c.Path)
		gotHolders = append(gotHolders, c.Holder)
	}
	t.Logf("输入=bob 推送 [free/1 a z/deep a/1] 实际输出=accepted=%v 冲突=%v 持有者=%v "+
		"判定依据=a/1(重复去重) a/2 z/deep/file 按字节序列出", res.Accepted, gotPaths, gotHolders)
	if res.Accepted {
		t.Fatal("应整批拒绝")
	}
	if !reflect.DeepEqual(gotPaths, []string{"a/1", "a/2", "z/deep/file"}) {
		t.Errorf("冲突路径应为 [a/1 a/2 z/deep/file]，得到 %v", gotPaths)
	}
	if !reflect.DeepEqual(gotHolders, []string{"alice", "alice", "carol"}) {
		t.Errorf("冲突持有者不正确: %v", gotHolders)
	}

	for _, p := range []string{"a/1", "a/2", "z/deep/file"} {
		if _, ok, _ := s.Lookup(p); !ok {
			t.Errorf("拒绝后锁 %q 不应改变", p)
		}
	}
	t.Logf("副作用核验 实际输出=三把锁均在 判定依据=拒绝不改变任何锁状态")

	res, err = s.ValidatePush("alice", []string{"a/1", "a/1/x", "free/9"}, false)
	t.Logf("输入=alice 推送 [a/1 a/1/x free/9] 实际输出=accepted=%v 判定依据=本人持锁与空闲路径放行",
		res.Accepted)
	if err != nil || !res.Accepted || len(res.Conflicts) != 0 {
		t.Errorf("本人持锁应放行: %+v, %v", res, err)
	}
}

// TestPushValidateAndRelease 验证「校验并顺带释放」：放行才释放，拒绝零副作用。
func TestPushValidateAndRelease(t *testing.T) {
	s := NewService()
	l1 := mustLock(t, s, "alice", "p/1")
	l2 := mustLock(t, s, "alice", "p/2")
	mustLock(t, s, "bob", "q/1")

	res, err := s.ValidatePush("alice", []string{"p/1", "p/2", "p/3"}, true)
	t.Logf("输入=alice 校验并释放 [p/1 p/2 p/3] 实际输出=accepted=%v released=%v "+
		"判定依据=整批放行时释放批内本人全部锁", res.Accepted, res.Released)
	if err != nil || !res.Accepted {
		t.Fatalf("应放行: %+v, %v", res, err)
	}
	if !reflect.DeepEqual(res.Released, []uint64{l1.ID, l2.ID}) {
		t.Errorf("释放列表应为 %v，得到 %v", []uint64{l1.ID, l2.ID}, res.Released)
	}
	if _, ok, _ := s.Lookup("p/1"); ok {
		t.Error("p/1 应已被释放")
	}
	if _, ok, _ := s.Lookup("q/1"); !ok {
		t.Error("他人的 q/1 不应受影响")
	}

	own := mustLock(t, s, "alice", "r/own")
	before := len(s.AuditLog())
	res, err = s.ValidatePush("alice", []string{"r/own", "q/1"}, true)
	t.Logf("输入=alice 校验并释放 [r/own q/1] 实际输出=accepted=%v released=%v "+
		"判定依据=q/1 被 bob 持有则整批拒绝且一把都不释放", res.Accepted, res.Released)
	if err != nil || res.Accepted || len(res.Released) != 0 {
		t.Errorf("应拒绝且零释放: %+v, %v", res, err)
	}
	got, ok, _ := s.Lookup("r/own")
	if !ok || got.ID != own.ID {
		t.Errorf("拒绝时本人锁 r/own 不应被释放: %+v ok=%v", got, ok)
	}
	if len(s.AuditLog()) != before {
		t.Error("被拒绝的校验不应产生审计记录")
	}
	t.Logf("副作用核验 实际输出=r/own 仍持有、审计数不变 判定依据=拒绝时零副作用")
}

// TestErrorPrecedence 验证错误次序中相邻错误的优先关系，
// 且被拒绝的调用不消耗锁序号。
// 注：「本人/他人持有 vs 祖先后代冲突」两对在任何可达状态下都不会同时适用
// （他人持锁互斥保证精确锁与他人祖先/后代锁不能共存），此处验证可构造的全部相邻对。
func TestErrorPrecedence(t *testing.T) {
	s := NewService("root")
	mustLock(t, s, "alice", "p/q")

	cases := []struct {
		name string
		call func() error
		want ErrorCode
		rule string
	}{
		{"参数非法>无权", func() error { return s.Unlock("", 1, true) }, ErrInvalidArgument,
			"空用户与无权强制同时适用时报参数非法"},
		{"无权>锁不存在", func() error { return s.Unlock("mallory", 99999, true) }, ErrPermissionDenied,
			"非管理员强制释放不存在的锁时报无权"},
		{"锁不存在>非持有者", func() error { return s.Unlock("bob", 99999, false) }, ErrLockNotFound,
			"锁不存在时无从判定持有者，报锁不存在"},
		{"参数非法>已由本人持有", func() error { _, e := s.Lock("alice", "p/q/../../.."); return e }, ErrInvalidArgument,
			"路径非法优先于任何持锁判定"},
		{"参数非法>已被他人持有", func() error { _, e := s.Lock("bob", "p/q/../../.."); return e }, ErrInvalidArgument,
			"路径非法优先于他人持有判定"},
		{"已由本人持有>祖先后代冲突(不可达)", func() error { _, e := s.Lock("alice", "p/q"); return e }, ErrHeldBySelf,
			"本人重复持锁报已由本人持有；他人祖先/后代锁与本锁互斥故不会同现"},
		{"已被他人持有>祖先后代冲突(不可达)", func() error { _, e := s.Lock("bob", "p/q"); return e }, ErrHeldByOther,
			"他人持精确锁报已被他人持有；同理祖先/后代冲突不会同现"},
	}
	for _, c := range cases {
		err := c.call()
		pe := mustErrCode(t, err, c.want)
		t.Logf("用例=%s 实际输出=%v 判定依据=%s", c.name, pe.Code, c.rule)
	}

	before, _, _ := s.Lookup("p/q")
	fresh := mustLock(t, s, "alice", "fresh")
	t.Logf("序号核验 被拒调用后新锁=%d 判定依据=被拒绝的调用不消耗序号", fresh.ID)
	if fresh.ID != before.ID+1 {
		t.Errorf("被拒绝的调用消耗了序号: 新锁 %d，期望 %d", fresh.ID, before.ID+1)
	}
}

// TestPushInvalidBatch 验证推送校验的参数非法形态。
func TestPushInvalidBatch(t *testing.T) {
	s := NewService()
	if _, err := s.ValidatePush("", []string{"a"}, false); errCodeOf(err) != ErrInvalidArgument {
		t.Errorf("空用户应报参数非法: %v", err)
	}
	if _, err := s.ValidatePush("alice", nil, false); errCodeOf(err) != ErrInvalidArgument {
		t.Errorf("空批次应报参数非法: %v", err)
	}
	mustLock(t, s, "bob", "c/1")
	_, err := s.ValidatePush("alice", []string{"c/1", "bad/../.."}, false)
	t.Logf("输入=批次含非法路径且有冲突 实际输出=%v 判定依据=参数非法优先于冲突裁决", err)
	if errCodeOf(err) != ErrInvalidArgument {
		t.Errorf("批内非法路径应报参数非法: %v", err)
	}
}

func errCodeOf(err error) ErrorCode {
	if pe, ok := err.(*Error); ok {
		return pe.Code
	}
	return 0
}

// TestPagination 验证前缀分页的字节序、游标续页，以及翻页期间的插入与删除。
func TestPagination(t *testing.T) {
	s := NewService()
	for _, p := range []string{"d/a", "d/b", "d/c", "d/e", "d/f", "other/x"} {
		mustLock(t, s, "alice", p)
	}

	page1, cur, more, err := s.ListByPrefix("d", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("第1页 实际输出=%v more=%v 判定依据=字节序最前的两条", pathsOf(page1), more)
	if !reflect.DeepEqual(pathsOf(page1), []string{"d/a", "d/b"}) || !more {
		t.Errorf("第1页错误: %v more=%v", pathsOf(page1), more)
	}

	// 翻页期间：插入 d/bb（落在游标后）与 d/z，删除 d/c，插入 d/aa（落在游标前）。
	mustLock(t, s, "alice", "d/bb")
	mustLock(t, s, "alice", "d/z")
	mustLock(t, s, "alice", "d/aa")
	lc, _, _ := s.Lookup("d/c")
	if err := s.Unlock("alice", lc.ID, false); err != nil {
		t.Fatal(err)
	}

	var rest []string
	for more {
		var page []Lock
		page, cur, more, err = s.ListByPrefix("d", cur, 2)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("续页 实际输出=%v more=%v 判定依据=从游标 %q 后继续", pathsOf(page), more, cur)
		rest = append(rest, pathsOf(page)...)
	}
	t.Logf("翻页期间变更后 实际输出=%v 判定依据=d/bb/d/z 完整出现，d/c 不出现，d/aa 不再出现，无重复",
		rest)
	want := []string{"d/bb", "d/e", "d/f", "d/z"}
	if !reflect.DeepEqual(rest, want) {
		t.Errorf("续页结果应为 %v，得到 %v", want, rest)
	}

	all, _, more, err := s.ListByPrefix("", "", 100)
	if err != nil || more {
		t.Fatalf("全量列举失败: %v more=%v", err, more)
	}
	t.Logf("空前缀 实际输出=%v 判定依据=空前缀匹配全部且按字节序", pathsOf(all))
	if got := pathsOf(all); !reflect.DeepEqual(got,
		[]string{"d/a", "d/aa", "d/b", "d/bb", "d/e", "d/f", "d/z", "other/x"}) {
		t.Errorf("全量列举错误: %v", got)
	}
}

func pathsOf(locks []Lock) []string {
	var out []string
	for _, l := range locks {
		out = append(out, l.Path)
	}
	return out
}
