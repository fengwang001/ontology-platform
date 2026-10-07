package ontology_test

import (
	"fmt"
	"testing"

	"ontology"
)

// 同一次提交影响多个聚合视图时的整体可见性：提交后两个视图必须同时反映
// 本次提交（同一 CommitSN 生效），不存在一个视图已更新、另一个未更新的状态。
func TestAtomicMultiViewVisibility(t *testing.T) {
	k := newKernel(t)
	op := 0

	w := ontology.Write{Type: typeOrder, Key: "o1", Prev: 0, Attrs: attrs(100, "us", "book")}
	op++
	res, err := k.Write(w)
	trace(t, op, "Write key=o1 amount=100 region=us category=book",
		fmt.Sprintf("v=%d sn=%d err=%v", res.Version, res.CommitSN, err),
		"成功提交必须分配版本号 1 与全局生效序号 1")
	if err != nil || res.Version != 1 || res.CommitSN != 1 {
		t.Fatalf("first write: %+v %v", res, err)
	}

	op++
	gr, gm, qerr := k.Query(viewByRegion, "us")
	gc, cm, _ := k.Query(viewByCategory, "book")
	trace(t, op, `Query(by_region,"us") + Query(by_category,"book")`,
		fmt.Sprintf("region=%v(meter=%+v) category=%v(meter=%+v) err=%v", gr, *gm, gc, *cm, qerr),
		"同一提交生效时刻后两个视图都必须计入 100；只一个视图可见即为可见性撕裂")
	if gr.Sum != 100 || gc.Sum != 100 || gr.Members != 1 || gc.Members != 1 {
		t.Fatalf("multi-view not atomically visible: %v %v", gr, gc)
	}

	w2 := ontology.Write{Type: typeOrder, Key: "o1", Prev: 1, Attrs: attrs(70, "eu", "game")}
	op++
	res2, err := k.Write(w2)
	trace(t, op, "Write key=o1 prev=1 amount=70 region=eu category=game",
		fmt.Sprintf("v=%d sn=%d err=%v", res2.Version, res2.CommitSN, err),
		"双视图分组迁移必须在同一临界区生效，sn 单调为 2")
	if err != nil || res2.Version != 2 || res2.CommitSN != 2 {
		t.Fatalf("second write: %+v %v", res2, err)
	}

	op++
	old1, _, _ := k.Query(viewByRegion, "us")
	new1, _, _ := k.Query(viewByRegion, "eu")
	old2, _, _ := k.Query(viewByCategory, "book")
	new2, _, _ := k.Query(viewByCategory, "game")
	trace(t, op, "Query old groups us/book and new groups eu/game",
		fmt.Sprintf("us=%v eu=%v book=%v game=%v", old1, new1, old2, new2),
		"旧分组 us/book 必须为 0 成员，新分组 eu/game 必须各为 70，二者同时成立")
	if old1.Sum != 0 || old1.Members != 0 || old2.Sum != 0 || old2.Members != 0 ||
		new1.Sum != 70 || new1.Members != 1 || new2.Sum != 70 || new2.Members != 1 {
		t.Fatalf("atomic migration across views failed: %v %v %v %v", old1, new1, old2, new2)
	}
}

// 乐观并发凭证的交错竞争：两个写者持有同一前序版本，只有一个成功，
// 另一个被拒绝且不占用版本号；失败后持新凭证重试可成功。
func TestOCCInterleaving(t *testing.T) {
	k := newKernel(t)
	op := 0

	base, err := k.Write(ontology.Write{Type: typeOrder, Key: "o1", Prev: 0, Attrs: attrs(1, "us", "book")})
	op++
	trace(t, op, "Write key=o1 prev=0 amount=1", fmt.Sprintf("res=%+v err=%v", base, err),
		"建立基线 v=1，两个竞争者都只知道 prev=1")
	if err != nil {
		t.Fatal(err)
	}

	op++
	winner, errW := k.Write(ontology.Write{Type: typeOrder, Key: "o1", Prev: 1, Attrs: attrs(11, "us", "book")})
	trace(t, op, "Write(竞争者A) key=o1 prev=1 amount=11", fmt.Sprintf("res=%+v err=%v", winner, errW),
		"A 首先通过凭证仲裁，成功提交 v=2")
	if errW != nil || winner.Version != 2 {
		t.Fatalf("winner: %+v %v", winner, errW)
	}

	op++
	loser, errL := k.Write(ontology.Write{Type: typeOrder, Key: "o1", Prev: 1, Attrs: attrs(99, "eu", "game")})
	trace(t, op, "Write(竞争者B) key=o1 prev=1 amount=99", fmt.Sprintf("res=%+v err=%v", loser, errL),
		"B 凭证停留在 v=1 而当前已是 v=2 -> version_conflict；拒绝不得占用版本号")
	if kindOf(errL) != ontology.ErrVersionConflict || loser.Version != 0 {
		t.Fatalf("loser: %+v %v", loser, errL)
	}

	op++
	cur, _ := k.GetInstance(typeOrder, "o1")
	trace(t, op, "GetInstance key=o1", fmt.Sprintf("rec=%+v", cur),
		"拒绝不占用版本号：当前仍是 v=2，属性仍是 A 的 amount=11/region=us")
	if cur.Version != 2 || cur.Attrs["amount"].Num != 11 || cur.Attrs["region"].Str != "us" {
		t.Fatalf("rejected write leaked into instance: %+v", cur)
	}

	op++
	retry, errR := k.Write(ontology.Write{Type: typeOrder, Key: "o1", Prev: 2, Attrs: attrs(99, "eu", "game")})
	trace(t, op, "Write(竞争者B重试) key=o1 prev=2 amount=99 region=eu category=game",
		fmt.Sprintf("res=%+v err=%v", retry, errR),
		"以新凭证 v=2 重试成功得到 v=3；聚合 us 扣除 11、eu 计入 99")
	if errR != nil || retry.Version != 3 {
		t.Fatalf("retry: %+v %v", retry, errR)
	}
	us, _, _ := k.Query(viewByRegion, "us")
	eu, _, _ := k.Query(viewByRegion, "eu")
	if us.Sum != 0 || us.Members != 0 || eu.Sum != 99 || eu.Members != 1 {
		t.Fatalf("post-retry aggregates wrong: us=%+v eu=%+v", us, eu)
	}
}

// 被拒绝的操作不得影响任何聚合视图：三类拒绝前后全部聚合取值必须一致，
// 且错误必须严格按「参数非法 -> 凭证不匹配 -> 不存在」次序只报第一个命中。
func TestRejectionOrderingAndIsolation(t *testing.T) {
	k := newKernel(t)
	op := 0
	if _, err := k.Write(ontology.Write{Type: typeOrder, Key: "o1", Prev: 0, Attrs: attrs(10, "us", "book")}); err != nil {
		t.Fatal(err)
	}
	beforeR := k.RecomputeView(viewByRegion)
	beforeC := k.RecomputeView(viewByCategory)
	op++
	trace(t, op, "建立 o1=10/us/book 基线", fmt.Sprintf("region=%+v category=%+v", beforeR["us"], beforeC["book"]),
		"记录拒绝前两个视图的全量快照，拒绝后必须逐字节一致")

	type bad struct {
		name string
		want ontology.ErrorKind
		call func() error
	}
	cases := []bad{
		{"空主键写入(同时凭证也是错的)", ontology.ErrInvalidArgument, func() error {
			_, e := k.Write(ontology.Write{Type: typeOrder, Key: "", Prev: 999, Attrs: attrs(1, "us", "book")})
			return e
		}},
		{"分组键为空", ontology.ErrInvalidArgument, func() error {
			_, e := k.Write(ontology.Write{Type: typeOrder, Key: "x", Prev: 0, Attrs: attrs(1, "", "book")})
			return e
		}},
		{"属性类型不符", ontology.ErrInvalidArgument, func() error {
			a := attrs(1, "us", "book")
			a["amount"] = ontology.Value{Str: "ten", Type: ontology.TypeString}
			_, e := k.Write(ontology.Write{Type: typeOrder, Key: "x", Prev: 0, Attrs: a})
			return e
		}},
		{"对已存在键用过期凭证(参数合法)", ontology.ErrVersionConflict, func() error {
			_, e := k.Write(ontology.Write{Type: typeOrder, Key: "o1", Prev: 0, Attrs: attrs(2, "us", "book")})
			return e
		}},
		{"删除不存在键但凭证错误 -> 冲突优先于不存在", ontology.ErrVersionConflict, func() error {
			_, e := k.Delete(ontology.Delete{Type: typeOrder, Key: "ghost", Prev: 5})
			return e
		}},
		{"删除不存在键且凭证为0 -> not_found", ontology.ErrNotFound, func() error {
			_, e := k.Delete(ontology.Delete{Type: typeOrder, Key: "ghost", Prev: 0})
			return e
		}},
	}
	for _, c := range cases {
		op++
		err := c.call()
		trace(t, op, c.name, errLabel(err),
			fmt.Sprintf("拒绝次序断言：错误类别必须为 %s，且聚合快照不变", c.want))
		if kindOf(err) != c.want {
			t.Fatalf("%s: want %s, got %v", c.name, c.want, err)
		}
	}

	afterR := k.RecomputeView(viewByRegion)
	afterC := k.RecomputeView(viewByCategory)
	op++
	trace(t, op, "拒绝风暴后 RecomputeView 两个视图",
		fmt.Sprintf("region=%+v category=%+v", afterR["us"], afterC["book"]),
		"与拒绝前快照逐分组比较：被拒绝操作不得影响任何聚合当前取值")
	if len(afterR) != len(beforeR) || len(afterC) != len(beforeC) {
		t.Fatalf("aggregate group set changed after rejections")
	}
	for g, v := range beforeR {
		if afterR[g] != v {
			t.Fatalf("region view changed for %s: %+v != %+v", g, afterR[g], v)
		}
	}
	for g, v := range beforeC {
		if afterC[g] != v {
			t.Fatalf("category view changed for %s: %+v != %+v", g, afterC[g], v)
		}
	}
}

// 分组迁移前后聚合取值的互斥归属：同一实例任一时刻只属于新/旧中的一个分组。
func TestGroupMigrationExclusivity(t *testing.T) {
	k := newKernel(t)
	op := 0
	mustWrite := func(prev int64, key string, amount float64, region, category string) ontology.CommitResult {
		t.Helper()
		op++
		res, err := k.Write(ontology.Write{Type: typeOrder, Key: key, Prev: prev, Attrs: attrs(amount, region, category)})
		trace(t, op, fmt.Sprintf("Write key=%s prev=%d amount=%v region=%s category=%s", key, prev, amount, region, category),
			fmt.Sprintf("res=%+v err=%v", res, err),
			"迁移成功后旧组扣除与新组计入是同一事件，两组成员数之和守恒")
		if err != nil {
			t.Fatalf("write: %v", err)
		}
		return res
	}

	mustWrite(0, "a", 10, "us", "book")
	mustWrite(0, "b", 20, "us", "game")
	mustWrite(1, "a", 10, "eu", "book") // a: us -> eu

	us := k.RecomputeView(viewByRegion)["us"]
	eu := k.RecomputeView(viewByRegion)["eu"]
	op++
	trace(t, op, "RecomputeView(by_region) groups us,eu", fmt.Sprintf("us=%+v eu=%+v", us, eu),
		"互斥归属：us 只剩 b=20/1 成员，eu 恰有 a=10/1 成员；总 SUM 守恒为 30")
	if us.Sum != 20 || us.Members != 1 || eu.Sum != 10 || eu.Members != 1 {
		t.Fatalf("migration exclusivity broken: us=%+v eu=%+v", us, eu)
	}

	op++
	res, err := k.Delete(ontology.Delete{Type: typeOrder, Key: "a", Prev: 2})
	trace(t, op, "Delete key=a prev=2", fmt.Sprintf("res=%+v err=%v", res, err),
		"删除生效后 eu 分组必须立即消失（成员 0），无残留贡献")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	euAfter, _, _ := k.Query(viewByRegion, "eu")
	if euAfter.Members != 0 || euAfter.Sum != 0 {
		t.Fatalf("stale contribution after delete: %+v", euAfter)
	}
}

// 对从未提交过的主键删除（ErrNotFound）与对已删除主键再次删除
// （ErrAlreadyDeleted）必须是可相互区分的两类错误。
func TestDeleteErrorDistinction(t *testing.T) {
	k := newKernel(t)
	op := 0

	op++
	_, err := k.Delete(ontology.Delete{Type: typeOrder, Key: "ghost", Prev: 0})
	trace(t, op, "Delete key=ghost prev=0 (从未提交过)", errLabel(err),
		"凭证 0==当前版本 0，但主键从未成功提交 -> 必须报 not_found")
	if kindOf(err) != ontology.ErrNotFound {
		t.Fatalf("want not_found, got %v", err)
	}

	op++
	res, err := k.Write(ontology.Write{Type: typeOrder, Key: "o1", Prev: 0, Attrs: attrs(5, "us", "book")})
	trace(t, op, "Write key=o1 prev=0 amount=5", fmt.Sprintf("res=%+v err=%v", res, err), "先成功提交 v=1")
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	op++
	d1, err := k.Delete(ontology.Delete{Type: typeOrder, Key: "o1", Prev: 1})
	trace(t, op, "Delete key=o1 prev=1", fmt.Sprintf("res=%+v err=%v", d1, err), "首次删除成功，墓碑版本 v=2")
	if err != nil || d1.Version != 2 {
		t.Fatalf("first delete: %+v %v", d1, err)
	}

	op++
	_, err = k.Delete(ontology.Delete{Type: typeOrder, Key: "o1", Prev: 2})
	trace(t, op, "Delete key=o1 prev=2 (已删除后再次删除)", errLabel(err),
		"凭证匹配但实例已是墓碑 -> 必须报 already_deleted，与 not_found 区分")
	if kindOf(err) != ontology.ErrAlreadyDeleted {
		t.Fatalf("want already_deleted, got %v", err)
	}

	op++
	_, err = k.Delete(ontology.Delete{Type: typeOrder, Key: "ghost", Prev: 0})
	trace(t, op, "Delete key=ghost prev=0 (再次确认从未提交)", errLabel(err),
		"始终是 not_found，两类错误在同一运行中同时出现且互不混淆")
	if kindOf(err) != ontology.ErrNotFound {
		t.Fatalf("want not_found again, got %v", err)
	}

	g, _, _ := k.Query(viewByRegion, "us")
	if g.Sum != 0 || g.Members != 0 {
		t.Fatalf("deleted instance contribution remains: %+v", g)
	}
}
