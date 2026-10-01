package layout_test

import (
	"errors"
	"testing"

	"ontology/layout"
)

func name(s string) []byte { return []byte(s) }

func locs(s *layout.StatInfo) []layout.Location {
	out := make([]layout.Location, len(s.Xattrs))
	for i, x := range s.Xattrs {
		out[i] = x.Location
	}
	return out
}

func requireErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

// 属性占用恰等于 inode 内剩余：放得下；超 1：落外部。
func TestExactFitAndOverByOne(t *testing.T) {
	m := layout.New(16, 64, 8, 10)
	f, err := m.Create()
	if err != nil || f != 1 {
		t.Fatalf("create: %v %d", err, f)
	}
	// 4+4+8=16，恰等于剩余 16。
	if err := m.SetXattr(f, name("a"), bytesN(5)); err != nil {
		t.Fatal(err)
	}
	s, _ := m.Stat(f)
	if locs(s)[0] != layout.InInode || m.PoolUsed() != 0 {
		t.Fatalf("exact fit: loc=%v pool=%d", locs(s), m.PoolUsed())
	}
	// A=19：同一属性占 16 <= 19 放得下；覆盖为值 9 字节 => 20 超剩余 1 => 外置。
	m2 := layout.New(19, 64, 8, 10)
	f2, _ := m2.Create()
	if err := m2.SetXattr(f2, name("a"), bytesN(9)); err != nil {
		t.Fatal(err)
	}
	s2, _ := m2.Stat(f2)
	if s2.Mode != layout.Inline || locs(s2)[0] != layout.External || s2.ExtID != f2 {
		t.Fatalf("over-by-one: %+v loc=%v", s2, locs(s2))
	}
	if m2.PoolUsed() != 1 {
		t.Fatalf("pool=%d", m2.PoolUsed())
	}
}

// 遇到第一个放不下的属性即停止，其后更小的属性也外置。
func TestFirstFailureStops(t *testing.T) {
	m := layout.New(16, 64, 8, 10)
	f, _ := m.Create()
	if err := m.SetXattr(f, name("a"), bytesN(5)); err != nil {
		t.Fatal(err)
	}
	if err := m.SetXattr(f, name("b"), bytesN(5)); err != nil {
		t.Fatal(err)
	}
	s, _ := m.Stat(f)
	if got := locs(s); got[0] != layout.InInode || got[1] != layout.External {
		t.Fatalf("locs=%v", got)
	}
	// c 占 4+4+0=8 很小，但因 b 已失败而一并外置。
	if err := m.SetXattr(f, name("c"), nil); err != nil {
		t.Fatal(err)
	}
	s, _ = m.Stat(f)
	if got := locs(s); got[0] != layout.InInode || got[1] != layout.External || got[2] != layout.External {
		t.Fatalf("locs=%v", got)
	}
	if m.PoolUsed() != 1 { // Ext = b(16)+c(8)=24
		t.Fatalf("pool=%d", m.PoolUsed())
	}
}

// 内联数据增大使属性外置乃至转块模式；块模式缩小不回内联，缩 0 回内联。
func TestInlineGrowAndBlockTransition(t *testing.T) {
	m := layout.New(16, 64, 8, 10)
	f, _ := m.Create()
	if err := m.SetXattr(f, name("a"), bytesN(5)); err != nil {
		t.Fatal(err)
	}
	if err := m.Resize(f, 1); err != nil {
		t.Fatal(err)
	}
	s, _ := m.Stat(f)
	if s.Mode != layout.Inline || locs(s)[0] != layout.External {
		t.Fatalf("size1: %+v", s)
	}
	// size=17 > A => 强制转块，数据 ceil(17/8)=3 块；块模式剩余 A=16，属性回 inode。
	if err := m.Resize(f, 17); err != nil {
		t.Fatal(err)
	}
	s, _ = m.Stat(f)
	if s.Mode != layout.Block || s.DataUsed != 3 || locs(s)[0] != layout.InInode {
		t.Fatalf("size17 block: %+v loc=%v", s, locs(s))
	}
	// 块模式缩小到 8（<=A）不回内联。
	if err := m.Resize(f, 8); err != nil {
		t.Fatal(err)
	}
	s, _ = m.Stat(f)
	if s.Mode != layout.Block || s.DataUsed != 1 || locs(s)[0] != layout.InInode {
		t.Fatalf("shrink block: %+v", s)
	}
	// 缩到 0 回内联，属性回 inode，块全释放。
	if err := m.Resize(f, 0); err != nil {
		t.Fatal(err)
	}
	s, _ = m.Stat(f)
	if s.Mode != layout.Inline || s.DataUsed != 0 || locs(s)[0] != layout.InInode || s.ExtID != 0 {
		t.Fatalf("back inline: %+v", s)
	}
	if m.PoolUsed() != 0 {
		t.Fatalf("pool=%d", m.PoolUsed())
	}

	// 恰好不转：无属性时 Resize(16) 合法且保持内联。
	m2 := layout.New(16, 64, 8, 10)
	g, _ := m2.Create()
	if err := m2.Resize(g, 16); err != nil {
		t.Fatal(err)
	}
	sg, _ := m2.Stat(g)
	if sg.Mode != layout.Inline || sg.DataUsed != 0 {
		t.Fatalf("exact no-transition: %+v", sg)
	}
	if err := m2.Resize(g, 17); err != nil {
		t.Fatal(err)
	}
	sg, _ = m2.Stat(g)
	if sg.Mode != layout.Block || sg.DataUsed != 3 {
		t.Fatalf("17 => block: %+v", sg)
	}

	// 因外置不合法而转块：X=12，size=1 时属性 16 外置超过 X，
	// 内联放置不合法 => 转块；块模式 inode 剩余 16 恰放得下，数据占 1 块。
	m3 := layout.New(16, 12, 8, 10)
	h, _ := m3.Create()
	if err := m3.Resize(h, 1); err != nil {
		t.Fatal(err)
	}
	if err := m3.SetXattr(h, name("a"), bytesN(5)); err != nil {
		t.Fatal(err)
	}
	sh, _ := m3.Stat(h)
	if sh.Mode != layout.Block || sh.DataUsed != 1 || locs(sh)[0] != layout.InInode {
		t.Fatalf("illegal-external => block: %+v loc=%v", sh, locs(sh))
	}
}

// 外部总字节恰等于 X 合法；超界（最小情形恰超 1）拒绝且不改状态。
func TestExternalXLimit(t *testing.T) {
	// X=15：两个最小属性（各 8）合计 16，超 1。
	mOver := layout.New(4, 15, 8, 10)
	fOver, _ := mOver.Create()
	if err := mOver.SetXattr(fOver, name("a"), nil); err != nil {
		t.Fatal(err)
	}
	requireErrIs(t, mOver.SetXattr(fOver, name("b"), nil), layout.ErrXattrTooLarge)
	sOver, _ := mOver.Stat(fOver)
	if len(sOver.Xattrs) != 1 {
		t.Fatalf("over-by-one rejection changed state")
	}

	// A=4 使所有属性都外置；X=20：a 空值 8 + b 4 字节值 12 = 20 恰等于 X。
	m := layout.New(4, 20, 8, 10)
	f, _ := m.Create()
	if err := m.SetXattr(f, name("a"), nil); err != nil { // 8
		t.Fatal(err)
	}
	if err := m.SetXattr(f, name("b"), bytesN(4)); err != nil { // 4+4+4=12；合计 20 == X
		t.Fatal(err)
	}
	s, _ := m.Stat(f)
	if got := locs(s); got[0] != layout.External || got[1] != layout.External {
		t.Fatalf("locs=%v", got)
	}
	// 再加属性 => 外部 > X；块模式剩余也仅 4 => 拒绝。
	requireErrIs(t, m.SetXattr(f, name("c"), nil), layout.ErrXattrTooLarge)
	s, _ = m.Stat(f)
	if len(s.Xattrs) != 2 {
		t.Fatalf("state changed after rejection: %d attrs", len(s.Xattrs))
	}
	// b 改 5 字节值 => roundup4(5)=8，b=16，总 24 > 20，拒绝且旧值（4 字节）保留。
	requireErrIs(t, m.SetXattr(f, name("b"), bytesN(5)), layout.ErrXattrTooLarge)
	v, _ := m.GetXattr(f, name("b"))
	if len(v) != 4 {
		t.Fatalf("overwrite leaked: value len=%d", len(v))
	}
}

// Ext 相同共享、改动后分裂、最后一个使用者改变后释放。
func TestExtShareSplitRelease(t *testing.T) {
	m := layout.New(16, 64, 8, 10)
	f1, _ := m.Create()
	f2, _ := m.Create()
	for _, f := range []int{f1, f2} {
		if err := m.Resize(f, 1); err != nil {
			t.Fatal(err)
		}
		if err := m.SetXattr(f, name("a"), bytesN(5)); err != nil {
			t.Fatal(err)
		}
	}
	s1, _ := m.Stat(f1)
	s2, _ := m.Stat(f2)
	if s1.ExtID != 1 || s2.ExtID != 1 || m.PoolUsed() != 1 {
		t.Fatalf("share: %d %d pool=%d", s1.ExtID, s2.ExtID, m.PoolUsed())
	}
	if err := m.SetXattr(f2, name("a"), bytesN(6)); err != nil { // Ext 分裂
		t.Fatal(err)
	}
	s1, _ = m.Stat(f1)
	s2, _ = m.Stat(f2)
	if s1.ExtID != 1 || s2.ExtID != 2 || m.PoolUsed() != 2 {
		t.Fatalf("split: %d %d pool=%d", s1.ExtID, s2.ExtID, m.PoolUsed())
	}
	if err := m.Resize(f2, 0); err != nil { // f2 释放其独占 Ext
		t.Fatal(err)
	}
	s2, _ = m.Stat(f2)
	if s2.ExtID != 0 || m.PoolUsed() != 1 {
		t.Fatalf("f2 release: ext=%d pool=%d", s2.ExtID, m.PoolUsed())
	}
	if err := m.Resize(f1, 0); err != nil {
		t.Fatal(err)
	}
	if m.PoolUsed() != 0 {
		t.Fatalf("final pool=%d", m.PoolUsed())
	}
}

// Clone 共享外部块、独立占数据块、属性值副本独立。
func TestClone(t *testing.T) {
	m := layout.New(16, 64, 8, 10)
	f1, _ := m.Create()
	if err := m.Resize(f1, 17); err != nil { // 块模式，3 数据块
		t.Fatal(err)
	}
	if err := m.SetXattr(f1, name("a"), bytesN(5)); err != nil {
		t.Fatal(err)
	}
	f2, err := m.Clone(f1)
	if err != nil || f2 != 2 {
		t.Fatalf("clone: %v %d", err, f2)
	}
	if m.PoolUsed() != 6 { // 数据块独立 3+3，Ext 空
		t.Fatalf("clone pool=%d", m.PoolUsed())
	}

	m2 := layout.New(16, 64, 8, 10)
	g1, _ := m2.Create()
	if err := m2.Resize(g1, 1); err != nil {
		t.Fatal(err)
	}
	if err := m2.SetXattr(g1, name("a"), bytesN(5)); err != nil {
		t.Fatal(err)
	}
	g2, _ := m2.Clone(g1)
	s1, _ := m2.Stat(g1)
	s2, _ := m2.Stat(g2)
	if s1.ExtID != g1 || s2.ExtID != g1 || m2.PoolUsed() != 1 {
		t.Fatalf("clone ext share: %d %d pool=%d", s1.ExtID, s2.ExtID, m2.PoolUsed())
	}
	if err := m2.SetXattr(g1, name("a"), bytesN(6)); err != nil {
		t.Fatal(err)
	}
	v, _ := m2.GetXattr(g2, name("a"))
	if len(v) != 5 {
		t.Fatalf("clone value not independent: %d", len(v))
	}
}

// 块池恰够与差 1，含共享使占用不增。
func TestPoolExactShortAndShare(t *testing.T) {
	// P=2：两个不同 Ext 恰够。
	m := layout.New(16, 64, 8, 2)
	f1, _ := m.Create()
	f2, _ := m.Create()
	if err := m.Resize(f1, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.SetXattr(f1, name("a"), bytesN(5)); err != nil {
		t.Fatal(err)
	}
	if err := m.Resize(f2, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.SetXattr(f2, name("a"), bytesN(6)); err != nil { // 第二个 Ext，恰够
		t.Fatal(err)
	}
	if m.PoolUsed() != 2 {
		t.Fatalf("exact pool=%d", m.PoolUsed())
	}
	f3, _ := m.Create()
	if err := m.Resize(f3, 1); err != nil {
		t.Fatal(err)
	}
	// 第三个不同 Ext => 差 1，拒绝。
	requireErrIs(t, m.SetXattr(f3, name("a"), bytesN(7)), layout.ErrPoolFull)
	// 与 f1 相同的 Ext => 共享，占用不增，成功。
	if err := m.SetXattr(f3, name("a"), bytesN(5)); err != nil {
		t.Fatalf("shared ext should succeed: %v", err)
	}
	if m.PoolUsed() != 2 {
		t.Fatalf("after share pool=%d", m.PoolUsed())
	}
	s3, _ := m.Stat(f3)
	if s3.ExtID != f1 {
		t.Fatalf("ext id=%d want %d", s3.ExtID, f1)
	}

	// 数据块：A=4、Bs=8、P=1，Resize(5)>A 转块恰占 1 块；第二个文件转块差 1 拒绝。
	m2 := layout.New(4, 64, 8, 1)
	g1, _ := m2.Create()
	if err := m2.Resize(g1, 5); err != nil {
		t.Fatal(err)
	}
	g2, _ := m2.Create()
	requireErrIs(t, m2.Resize(g2, 5), layout.ErrPoolFull)
	// g2 仍存在但大小 0、内联（Create 成功，Resize 被拒）。
	sg, _ := m2.Stat(g2)
	if sg.Mode != layout.Inline || sg.Size != 0 {
		t.Fatalf("rejected resize changed state: %+v", sg)
	}
}

// 同名覆盖改变占用与落点；GetXattr 返回副本。
func TestOverwriteAndCopy(t *testing.T) {
	m := layout.New(16, 64, 8, 10)
	f, _ := m.Create()
	if err := m.SetXattr(f, name("a"), bytesN(5)); err != nil {
		t.Fatal(err)
	}
	v, _ := m.GetXattr(f, name("a"))
	v[0] = 'X'
	v2, _ := m.GetXattr(f, name("a"))
	if v2[0] != 'v' {
		t.Fatal("GetXattr did not return a copy")
	}
	// size=0 时 20 字节仍可全部外置（20<=X），故保持内联、属性外置。
	if err := m.SetXattr(f, name("a"), bytesN(9)); err != nil {
		t.Fatal(err)
	}
	s, _ := m.Stat(f)
	if s.Mode != layout.Inline || locs(s)[0] != layout.External || s.ExtID != f {
		t.Fatalf("expected inline external after overwrite, %+v", s)
	}
	// 同名覆盖回小值：占用回落 16，size=0 时属性重新回到 inode 内。
	if err := m.SetXattr(f, name("a"), bytesN(5)); err != nil {
		t.Fatal(err)
	}
	s, _ = m.Stat(f)
	if locs(s)[0] != layout.InInode || s.ExtID != 0 {
		t.Fatalf("expected back in inode after overwrite, %+v", s)
	}
}

// 拒绝顺序与哨兵错误、被拒操作不改状态。
func TestErrorOrderAndAtomicity(t *testing.T) {
	m := layout.New(16, 64, 8, 10)
	// 参数非法优先于文件不存在。
	requireErrIs(t, m.Resize(99, -1), layout.ErrInvalidArgument)
	requireErrIs(t, m.SetXattr(99, nil, nil), layout.ErrInvalidArgument)
	requireErrIs(t, m.SetXattr(99, name(""), bytesN(1)), layout.ErrInvalidArgument)
	requireErrIs(t, m.SetXattr(99, make([]byte, 256), nil), layout.ErrInvalidArgument)
	requireErrIs(t, m.SetXattr(99, name("a"), make([]byte, 4097)), layout.ErrInvalidArgument)
	requireErrIs(t, m.Resize(99, 1), layout.ErrNotFound)
	_, err := m.Clone(99)
	requireErrIs(t, err, layout.ErrNotFound)
	_, err = m.Stat(99)
	requireErrIs(t, err, layout.ErrNotFound)
	f, _ := m.Create()
	_, err = m.GetXattr(f, name("nope"))
	requireErrIs(t, err, layout.ErrNoXattr)
	requireErrIs(t, m.RemoveXattr(f, name("nope")), layout.ErrNoXattr)

	// 放置不合法优先于块池不足：单个属性 4+roundup4(255)+roundup4(4096) 远超 X。
	huge := layout.New(16, 1, 8, 100)
	hf, _ := huge.Create()
	err = huge.SetXattr(hf, make([]byte, 255), make([]byte, 4096))
	requireErrIs(t, err, layout.ErrXattrTooLarge)
	sh, _ := huge.Stat(hf)
	if len(sh.Xattrs) != 0 {
		t.Fatal("rejected huge set changed state")
	}

	// 构造参数非法 => panic(ErrInvalidArgument)。
	func() {
		defer func() {
			r := recover()
			if r == nil || !errors.Is(r.(error), layout.ErrInvalidArgument) {
				t.Fatalf("panic = %v", r)
			}
		}()
		layout.New(0, 1, 1, 1)
	}()

	// 块池满时 Clone 被拒绝且不消耗编号：A=4、Bs=8、P=1，块模式文件占 1 数据块。
	full := layout.New(4, 64, 8, 1)
	busy, _ := full.Create()
	if err := full.Resize(busy, 5); err != nil { // 占满 1 块
		t.Fatal(err)
	}
	_, err = full.Clone(busy) // 克隆的数据块独立 => 需再 1 块 => 拒绝
	requireErrIs(t, err, layout.ErrPoolFull)
	next, err2 := full.Create()
	if err2 != nil || next != 2 {
		t.Fatalf("rejected clone consumed id: next=%d err=%v", next, err2)
	}
}
