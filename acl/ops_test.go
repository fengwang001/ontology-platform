package acl

import (
	"reflect"
	"testing"
)

func wantErr(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error kind %v, got nil", kind)
	}
	ae, ok := err.(*Error)
	if !ok {
		t.Fatalf("error %v is not *acl.Error", err)
	}
	if ae.Kind != kind {
		t.Fatalf("error kind = %v, want %v (err=%v)", ae.Kind, kind, err)
	}
}

func TestNewStoreValidation(t *testing.T) {
	for _, dk := range [][2]int{{0, 1}, {65, 1}, {1, 0}, {1, 65}, {-1, -1}} {
		if _, err := NewStore(dk[0], dk[1]); err == nil {
			t.Fatalf("NewStore(%d, %d) should fail", dk[0], dk[1])
		} else {
			wantErr(t, err, ErrInvalid)
		}
	}
	if _, err := NewStore(1, 1); err != nil {
		t.Fatalf("NewStore(1, 1): %v", err)
	}
	if _, err := NewStore(64, 64); err != nil {
		t.Fatalf("NewStore(64, 64): %v", err)
	}
}

func TestAddNodeErrors(t *testing.T) {
	s := mustStore(t, 2, 4)
	mustAdd(t, s, "obj", "/", false)
	mustAdd(t, s, "ctr", "/", true)
	mustAdd(t, s, "deep", "ctr", true)

	wantErr(t, s.AddNode("", "/", true), ErrInvalid)       // 空编号
	wantErr(t, s.AddNode("x", "ghost", true), ErrNotFound) // parent 不存在
	wantErr(t, s.AddNode("/", "/", true), ErrConflict)     // 编号为根
	wantErr(t, s.AddNode("obj", "/", true), ErrConflict)   // 编号重复
	wantErr(t, s.AddNode("y", "obj", true), ErrConflict)   // 父为对象
	wantErr(t, s.AddNode("z", "deep", true), ErrLimit)     // 深度超限（deep 深度 2，D=2）
	// 类别顺序：空编号优先于 parent 不存在。
	wantErr(t, s.AddNode("", "ghost", true), ErrInvalid)
	// parent 不存在优先于编号重复。
	wantErr(t, s.AddNode("obj", "ghost", true), ErrNotFound)
	// 编号重复优先于父为对象与深度超限。
	wantErr(t, s.AddNode("obj", "obj", true), ErrConflict)
	wantErr(t, s.AddNode("obj", "deep", true), ErrConflict)

	if v := s.Version(); v != 3 {
		t.Fatalf("Version = %d, want 3 (only successful adds)", v)
	}
}

func TestSetACLErrors(t *testing.T) {
	s := mustStore(t, 4, 2)
	good := ACE{Allow: true, Principal: "u", Mask: 1, Flags: 0}

	wantErr(t, s.SetACL("/", []ACE{{Allow: true, Principal: "", Mask: 1}}, false), ErrInvalid)
	wantErr(t, s.SetACL("/", []ACE{{Allow: true, Principal: "u", Mask: 0}}, false), ErrInvalid)
	wantErr(t, s.SetACL("/", []ACE{{Allow: true, Principal: "u", Mask: 65536}}, false), ErrInvalid)
	wantErr(t, s.SetACL("/", []ACE{{Allow: true, Principal: "u", Mask: 1, Flags: 16}}, false), ErrInvalid)
	wantErr(t, s.SetACL("/", []ACE{{Allow: true, Principal: "u", Mask: 1, Flags: FlagIO}}, false), ErrInvalid)
	wantErr(t, s.SetACL("/", []ACE{{Allow: true, Principal: "u", Mask: 1, Flags: FlagNP}}, false), ErrInvalid)
	wantErr(t, s.SetACL("ghost", []ACE{good}, false), ErrNotFound)
	wantErr(t, s.SetACL("/", []ACE{good, good, good}, false), ErrLimit)
	// 类别顺序：条目非法优先于节点不存在与超限。
	wantErr(t, s.SetACL("ghost", []ACE{{Principal: "", Mask: 1}, good, good}, false), ErrInvalid)
	// 节点不存在优先于超限。
	wantErr(t, s.SetACL("ghost", []ACE{good, good, good}, false), ErrNotFound)

	if v := s.Version(); v != 0 {
		t.Fatalf("Version = %d, want 0", v)
	}
	mustSetACL(t, s, "/", []ACE{good, good}, false)
	if v := s.Version(); v != 1 {
		t.Fatalf("Version = %d, want 1", v)
	}
}

func TestEvalArgErrors(t *testing.T) {
	s := mustStore(t, 4, 4)
	wantErr2 := func(err error) { wantErr(t, err, ErrInvalid) }
	_, err := s.Eval(nil, "/", 1)
	wantErr2(err)
	_, err = s.Eval([]string{"u", ""}, "/", 1)
	wantErr2(err)
	_, err = s.Eval([]string{"u"}, "/", 0)
	wantErr2(err)
	_, err = s.Eval([]string{"u"}, "/", 65536)
	wantErr2(err)
	_, err = s.Eval([]string{"u"}, "ghost", 1)
	wantErr(t, err, ErrNotFound)
	// 参数非法优先于节点不存在。
	_, err = s.Eval(nil, "ghost", 1)
	wantErr2(err)
	_, err = s.Effective("ghost")
	wantErr(t, err, ErrNotFound)
}

func TestMoveErrors(t *testing.T) {
	s := mustStore(t, 3, 4)
	mustAdd(t, s, "a", "/", true)
	mustAdd(t, s, "b", "a", true)
	mustAdd(t, s, "o", "/", false)

	wantErr(t, s.Move("ghost", "/"), ErrNotFound)      // node 不存在
	wantErr(t, s.Move("a", "ghost"), ErrNotFound)      // newParent 不存在
	wantErr(t, s.Move("ghost", "ghost2"), ErrNotFound) // 先查 node
	wantErr(t, s.Move("/", "a"), ErrConflict)          // 移动根
	wantErr(t, s.Move("a", "o"), ErrConflict)          // 新父为对象
	wantErr(t, s.Move("a", "a"), ErrConflict)          // 新父是自身
	wantErr(t, s.Move("a", "b"), ErrConflict)          // 新父是后代
	wantErr(t, s.Move("b", "a"), ErrConflict)          // 新父就是当前父
	wantErr(t, s.Move("a", "b"), ErrConflict)

	if v := s.Version(); v != 3 {
		t.Fatalf("Version = %d, want 3", v)
	}
}

// Move 的深度检查按整个子树的最大深度计。
func TestMoveDepthLimitIncludesSubtree(t *testing.T) {
	s := mustStore(t, 3, 4)
	mustAdd(t, s, "a", "/", true) // 深度 1
	mustAdd(t, s, "b", "a", true) // 深度 2
	mustAdd(t, s, "c", "b", true) // 深度 3
	mustAdd(t, s, "d", "/", true) // 深度 1
	mustAdd(t, s, "e", "d", true) // 深度 2
	// 把 a（子树最大深度 3）挂到 e（深度 2）下：a 变深度 3，c 变深度 5 > 3。
	wantErr(t, s.Move("a", "e"), ErrLimit)
	// 把 b（子树最大深度 3）挂到 / 下：b 变深度 1，c 变深度 2，合法。
	if err := s.Move("b", "/"); err != nil {
		t.Fatalf("Move(b, /): %v", err)
	}
	if v := s.Version(); v != 6 {
		t.Fatalf("Version = %d, want 6", v)
	}
}

// Move 后继承随新父改变。
func TestMoveChangesInheritance(t *testing.T) {
	s := mustStore(t, 6, 4)
	mustAdd(t, s, "p1", "/", true)
	mustAdd(t, s, "p2", "/", true)
	mustAdd(t, s, "c", "p1", true)
	mustSetACL(t, s, "p1", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: FlagCI},
	}, false)
	mustSetACL(t, s, "p2", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0010, Flags: FlagCI},
	}, false)

	checkResult(t, mustEval(t, s, []string{"u"}, "c", 0b0001), ResultGranted, 0, "p1", true, 0b0001)
	checkResult(t, mustEval(t, s, []string{"u"}, "c", 0b0010), ResultImplicitDeny, -1, "", false, 0)

	if err := s.Move("c", "p2"); err != nil {
		t.Fatalf("Move(c, p2): %v", err)
	}
	checkResult(t, mustEval(t, s, []string{"u"}, "c", 0b0010), ResultGranted, 0, "p2", true, 0b0010)
	checkResult(t, mustEval(t, s, []string{"u"}, "c", 0b0001), ResultImplicitDeny, -1, "", false, 0)
}

// 被拒绝的操作不改变任何可观察状态。
func TestRejectedOpsKeepState(t *testing.T) {
	s := mustStore(t, 2, 2)
	mustAdd(t, s, "a", "/", true)
	aces := []ACE{{Allow: true, Principal: "u", Mask: 1, Flags: FlagCI}}
	mustSetACL(t, s, "/", aces, false)
	before := mustEffective(t, s, "a")
	ver := s.Version()

	wantErr(t, s.AddNode("", "/", true), ErrInvalid)
	wantErr(t, s.AddNode("a", "/", true), ErrConflict)
	wantErr(t, s.SetACL("/", []ACE{{Principal: "x", Mask: 0}}, false), ErrInvalid)
	wantErr(t, s.SetACL("/", []ACE{aces[0], aces[0], aces[0]}, false), ErrLimit)
	wantErr(t, s.Move("a", "ghost"), ErrNotFound)
	wantErr(t, s.Move("a", "/"), ErrConflict)

	if s.Version() != ver {
		t.Fatalf("Version changed: %d -> %d", ver, s.Version())
	}
	after := mustEffective(t, s, "a")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("E(a) changed: %+v -> %+v", before, after)
	}
	rootEff := mustEffective(t, s, "/")
	if len(rootEff) != 1 || rootEff[0].Principal != "u" {
		t.Fatalf("E(/) changed: %+v", rootEff)
	}
}
