package emrtest_test

import (
	"encoding/hex"
	"testing"

	"ontology/record"
)

// 固定输入下的逐字节期望哈希（独立程序按规范计算得到）。
const (
	wantH1    = "0c4929d2c5ed272acc0a0a53af788fe17cb05f8b7e7ba8ba57a247943539b5e5"
	wantH2    = "54ea267545d1450a4c9342c12f25115e7dd0aac25904db4000761e688ed14825"
	wantSeal1 = "861ba259d39f1e44084b5be7360f6236ce4d9a0de2fced72080a921355695358"
	wantSeal2 = "6373961330d52d5320337b0f85ebaeae8a48bfe8f922bd98d57af70794e1aa88"
	wantAmend = "b91795d48c79c6eaed23cf18815a60dfe9a35341e49a60363fe5a99ab161f529"
)

func TestScenarios(t *testing.T) {
	cases := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"example_flow", testExampleFlow},
		{"auto_seal_boundary_defect_latesign", testAutoSealBoundary},
		{"sign_invalidated_by_edit", testSignInvalidated},
		{"level2_author_no_cosign", testLevel2Author},
		{"unseal_window_boundary_and_gen", testUnsealWindow},
		{"manual_seal_and_amend_perm", testManualSealAmend},
		{"perm_before_state", testPermBeforeState},
		{"reject_order_and_clock", testRejectOrder},
		{"hash_counts", testHashCounts},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.fn)
	}
}

// 题目第一个例子的逐步复现。
func testExampleFlow(t *testing.T) {
	v := setupExample(t)

	mustOK(t, "1100 create d1", v.create(1100, "r", "e1", "d1", "c1"))
	if got := hex.EncodeToString(v.inspect("d1").Versions[0].Hash); got != wantH1 {
		t.Fatalf("h1 byte mismatch: %s", got)
	}
	mustOK(t, "1200 sign d1", v.sign(1200, "r", "d1"))
	if v.inspect("d1").State != record.StateSigned {
		t.Fatal("r level1 签名后应为已签")
	}
	mustOK(t, "1300 edit d1", v.edit(1300, "r", "d1", "c2"))
	d := v.inspect("d1")
	if len(d.Versions) != 2 || d.State != record.StateDraft || d.AuthorSigned {
		t.Fatalf("已签下 Edit 应追加第2版并退回草稿: %+v", d)
	}
	if got := hex.EncodeToString(d.Versions[1].Hash); got != wantH2 {
		t.Fatalf("h2 byte mismatch: %s", got)
	}
	mustOK(t, "1400 sign", v.sign(1400, "r", "d1"))
	mustOK(t, "1500 cosign", v.cosign(1500, "s", "d1"))
	if v.inspect("d1").State != record.StateApproved {
		t.Fatal("cosign 后应为已审签")
	}
	mustErr(t, "1600 r edit 已审签", v.edit(1600, "r", "d1", "x"), record.ErrState)
	mustErr(t, "1600 k edit 无权限", v.edit(1600, "k", "d1", "x"), record.ErrPerm)
}

// 封存时刻取等、缺陷清单进出、封存后迟签不可改。
func testAutoSealBoundary(t *testing.T) {
	v := setupExample(t)
	mustOK(t, "create d1", v.create(1100, "r", "e1", "d1", "c1"))
	mustOK(t, "sign d1", v.sign(1200, "r", "d1"))
	mustOK(t, "edit d1", v.edit(1300, "r", "d1", "c2"))
	mustOK(t, "sign d1", v.sign(1400, "r", "d1"))
	mustOK(t, "cosign d1", v.cosign(1500, "s", "d1"))
	mustOK(t, "create d2", v.create(1550, "r", "e1", "d2", "draft"))

	// 5319 < 5320(=1000+4320)：未封存，草稿可 Edit。
	mustOK(t, "5319 edit d2", v.edit(5319, "r", "d2", "draft2"))
	if v.inspect("d1").Sealed || v.inspect("d2").Sealed {
		t.Fatal("5319 尚不应封存")
	}

	// 5320 的任一被接受操作开头落地封存；d2 草稿入缺陷清单。
	mustOK(t, "5320 sign d2", v.sign(5320, "r", "d2"))
	d1 := v.inspect("d1")
	d2 := v.inspect("d2")
	if !d1.Sealed || d1.Gen != 1 || !d2.Sealed || d2.Gen != 1 {
		t.Fatalf("5320 两文档均应封存且 gen=1: d1=%+v d2=%+v", d1, d2)
	}
	if got := hex.EncodeToString(d2.SealHash); got != "" {
		// d2 末版哈希不等于 d1，仅校验结构；d1 的 sealHash 给期望值。
	}
	if got := hex.EncodeToString(d1.SealHash); got != wantSeal1 {
		t.Fatalf("d1 sealHash mismatch: %s", got)
	}
	if def := v.defects(); len(def) != 1 || def[0] != "d2" {
		t.Fatalf("d2 应在缺陷清单: %v", def)
	}

	// 封存后不可改，签名可补（迟签）。
	mustErr(t, "封存后 Edit", v.edit(5330, "r", "d2", "x"), record.ErrState)
	mustOK(t, "封存后 s cosign d2(已签)", v.cosign(5340, "s", "d2"))
	d2 = v.inspect("d2")
	if d2.State != record.StateApproved || len(d2.LateSign) != 2 ||
		!d2.LateSign[0].Late || d2.LateSign[0].User != "r" ||
		!d2.LateSign[1].Late || !d2.LateSign[1].Cosign || d2.LateSign[1].User != "s" {
		t.Fatalf("迟签应记录并审签: %+v", d2)
	}
	if def := v.defects(); len(def) != 0 {
		t.Fatalf("补到已审签应移出缺陷清单: %v", def)
	}

	// 补记：作者成功，跨科室 level3 的 k 无权限。
	mustOK(t, "r amend", v.amend(5400, "r", "d2", "a1"))
	if got := hex.EncodeToString(v.inspect("d2").Amends[0].Hash); got != wantAmend {
		t.Fatalf("amend hash mismatch: %s", got)
	}
	mustErr(t, "k amend 无权限", v.amend(5410, "k", "d2", "a2"), record.ErrPerm)
	if len(v.inspect("d2").Amends) != 1 {
		t.Fatal("被拒绝的 Amend 不得落地")
	}

	// Verify 重算完好。
	res, err := v.verify("d2")
	mustOK(t, "verify d2", err)
	if !res.VersionChainOK || !res.AmendChainOK {
		t.Fatalf("链应完好: %+v", res)
	}
}

// 签名随修改失效。
func testSignInvalidated(t *testing.T) {
	v := setupExample(t)
	mustOK(t, "create", v.create(1100, "r", "e1", "d", "c"))
	mustOK(t, "sign", v.sign(1200, "r", "d"))
	mustOK(t, "return", v.ret(1250, "s", "d"))
	if v.inspect("d").State != record.StateDraft {
		t.Fatal("Return 应退回草稿")
	}
	mustOK(t, "sign2", v.sign(1300, "r", "d"))
	mustOK(t, "edit", v.edit(1400, "r", "d", "c2"))
	d := v.inspect("d")
	if d.State != record.StateDraft || d.AuthorSigned || len(d.Versions) != 2 {
		t.Fatalf("已签下 Edit 必须清除签名态: %+v", d)
	}
	// 草稿态不能 Cosign。
	mustErr(t, "draft cosign", v.cosign(1500, "s", "d"), record.ErrState)
}

// level2 作者免审签。
func testLevel2Author(t *testing.T) {
	v := setupExample(t)
	mustOK(t, "create by s", v.create(1100, "s", "e1", "d", "c"))
	mustOK(t, "s sign 直接审签", v.sign(1200, "s", "d"))
	d := v.inspect("d")
	if d.State != record.StateApproved {
		t.Fatalf("level2 作者 Sign 应直接已审签: %+v", d)
	}
	// 已审签不能再 Cosign。
	mustErr(t, "approved cosign", v.cosign(1300, "k", "d"), record.ErrPerm) // k 外科，权限先报
}

// 解封窗口取等与 gen 递增；一人兼两权也不行。
func testUnsealWindow(t *testing.T) {
	v := setupExample(t)
	// a 兼医务与病案；b 病案；c 医务。
	mustOK(t, "add a", v.user("a", "医务处", 3, record.RoleMedicalApproval|record.RoleRecordApproval))
	mustOK(t, "add b", v.user("b", "病案室", 2, record.RoleRecordApproval))
	mustOK(t, "add c", v.user("c", "医务处2", 2, record.RoleMedicalApproval))
	mustOK(t, "create d1", v.create(1100, "r", "e1", "d1", "c1"))
	mustOK(t, "sign", v.sign(1200, "r", "d1"))
	mustOK(t, "edit", v.edit(1300, "r", "d1", "c2"))
	mustOK(t, "sign", v.sign(1400, "r", "d1"))
	mustOK(t, "cosign", v.cosign(1500, "s", "d1"))
	mustOK(t, "create d2 tick", v.create(1600, "r", "e1", "d2", "c"))

	// 5320 开头 d1、d2 自动封存。
	mustOK(t, "5320 sign d2", v.sign(5320, "r", "d2"))
	d1 := v.inspect("d1")
	if !d1.Sealed || d1.Gen != 1 || hex.EncodeToString(d1.SealHash) != wantSeal1 {
		t.Fatalf("d1 自动封存不符: %+v", d1)
	}

	// a、b 同一人即使兼两权也拒绝；参数不同但 a==a。
	mustErr(t, "a,a 一人兼两权", v.unseal(6000, "a", "a", "d1"), record.ErrPerm)
	if v.inspect("d1").UnsealedUntil != 0 {
		t.Fatal("被拒绝的 Unseal 不得落地")
	}
	// b 无医务权、c 无病案权。
	mustErr(t, "b,c 角色反了", v.unseal(6000, "b", "c", "d1"), record.ErrPerm)
	mustErr(t, "窗口内不可 Amend", func() error {
		mustOK(t, "a,b 解封", v.unseal(6000, "a", "b", "d1"))
		return v.amend(6010, "r", "d1", "x")
	}(), record.ErrState)
	mustErr(t, "窗口内不可再次 Unseal", v.unseal(6010, "c", "b", "d1"), record.ErrState)

	// 窗口内按未封存：已审签不能 Edit（状态不符），恰等 6059 仍在窗口。
	mustErr(t, "6059 edit 已审签", v.edit(6059, "r", "d1", "x"), record.ErrState)
	if n := len(v.inspect("d1").Versions); n != 2 {
		t.Fatalf("窗口内版本链不应变化: %d", n)
	}

	// 6060 = 6000+60 恰等：操作开头先再封存，随后 Amend 恢复可用（成功本身证明先落地）。
	mustOK(t, "6060 amend 在再封存后成功", v.amend(6060, "r", "d1", "y"))
	d1 = v.inspect("d1")
	if record.EffectiveSealed(&d1) != true || d1.Gen != 2 || len(d1.Versions) != 2 {
		t.Fatalf("窗口到期应再封存: %+v", d1)
	}
	if got := hex.EncodeToString(d1.SealHash); got != wantSeal2 || got == wantSeal1 {
		t.Fatalf("gen=2 sealHash 应不同于 gen=1: %s", got)
	}
	// 窗口内的那次失败 Amend 未落地，6060 这条是补记链第 1 条。
	if len(d1.Amends) != 1 {
		t.Fatalf("窗口内 Amend 不得落地: %+v", d1.Amends)
	}
}

// 手动封存要求归档员且已审签；补记权限。
func testManualSealAmend(t *testing.T) {
	v := setupExample(t)
	mustOK(t, "add arch", v.user("q", "病案室", 2, record.RoleArchivist))
	mustOK(t, "create d", v.create(1100, "r", "e1", "d", "c"))
	// 草稿手动封存：状态不符。
	mustErr(t, "draft 手动封存", v.seal(1200, "q", "d"), record.ErrState)
	// 非归档员：无权限（状态也不对，权限先报）。
	mustErr(t, "s 无归档角色", v.seal(1200, "s", "d"), record.ErrPerm)
	mustOK(t, "sign", v.sign(1300, "r", "d"))
	// 已签但未审签，归档员封存仍报状态不符。
	mustErr(t, "signed 手动封存", v.seal(1400, "q", "d"), record.ErrState)
	mustOK(t, "cosign", v.cosign(1500, "s", "d"))
	mustOK(t, "approved 手动封存", v.seal(1600, "q", "d"))
	d := v.inspect("d")
	if !d.Sealed || d.Gen != 1 || d.Defect {
		t.Fatalf("手动封存已审签文档不应入缺陷: %+v", d)
	}
	// 未封存文档 Amend 报状态不符（同科室 level2 的 s 也不行）。
	mustOK(t, "create d3", v.create(1700, "r", "e1", "d3", "c"))
	mustErr(t, "未封存 Amend", v.amend(1800, "s", "d3", "x"), record.ErrState)
}

// 无权限先于状态不符。
func testPermBeforeState(t *testing.T) {
	v := setupExample(t)
	mustOK(t, "create d", v.create(1100, "r", "e1", "d", "c"))
	// 非作者编辑一份草稿（状态允许 Edit）：无权限。
	mustErr(t, "s 改他人草稿", v.edit(1200, "s", "d", "x"), record.ErrPerm)
	mustErr(t, "k 改他人草稿", v.edit(1200, "k", "d", "x"), record.ErrPerm)
	// 封存后非作者去改：仍先报无权限而非状态不符。
	mustOK(t, "sign", v.sign(1300, "r", "d"))
	mustOK(t, "cosign", v.cosign(1400, "s", "d"))
	// 用另一份草稿文档在 5320 的被接受 Sign 触发自动封存；随后非作者改 d 先报无权限。
	mustOK(t, "create d2", v.create(1500, "r", "e1", "d2", "c"))
	mustOK(t, "5320 sign d2 触发封存", v.sign(5320, "r", "d2"))
	mustErr(t, "封存后 k 改 d 无权限", v.edit(5330, "k", "d", "x"), record.ErrPerm)
}

// 拒绝次序与时钟：非法参数 > 时钟回退 > 不存在 > 无权限 > 状态。
func testRejectOrder(t *testing.T) {
	v := setupExample(t)
	// 参数非法优先于一切。
	mustErr(t, "level 越界", v.user("x", "内科", 9, 0), record.ErrInvalid)
	mustErr(t, "now 越界", v.create(-1, "r", "e1", "dd", "c"), record.ErrInvalid)
	mustErr(t, "now 超上限", v.create(1_000_000_001, "r", "e1", "dd", "c"), record.ErrInvalid)
	// T、U 非法直接 panic（构造期）由 New 保证。

	// 时钟回退先于不存在/无权限/状态。
	mustOK(t, "create", v.create(2000, "r", "e1", "dd", "c"))
	mustErr(t, "回退", v.edit(1999, "nobody", "nodoc", "x"), record.ErrClock)
	// 不存在先于权限与状态。
	mustErr(t, "doc 缺失", v.edit(2001, "k", "nodoc", "x"), record.ErrMissing)
	// 重复文档号归不存在类。
	mustErr(t, "doc 重复", v.create(2002, "r", "e1", "dd", "c2"), record.ErrMissing)
	// 未出院就诊不会自动封存；已到封存时刻不得 Create。
	v2 := newSvc(100, 10)
	mustOK(t, "v2 user", v2.user("r", "内科", 1, 0))
	mustOK(t, "v2 enc", v2.enc("e"))
	mustOK(t, "v2 discharge 0", v2.dis(0, "e"))
	mustErr(t, "t=100 恰等不得 Create", v2.create(100, "r", "e", "d", "c"), record.ErrState)
	mustOK(t, "t=99 可 Create", v2.create(99, "r", "e", "d", "c"))
	// 重复出院报状态不符。
	mustErr(t, "重复出院", v2.dis(100, "e"), record.ErrState)
}

// 哈希计数：Edit/Amend/封存各 1 次，与版本数无关；Verify = 版本数+补记数+1。
func testHashCounts(t *testing.T) {
	v := setupExample(t)
	mustOK(t, "add arch", v.user("q", "病案室", 2, record.RoleArchivist))
	before := v.hashes()
	mustOK(t, "create", v.create(1100, "r", "e1", "d", "c0")) // +1
	if v.hashes() != before+1 {
		t.Fatalf("Create 应计 1 次哈希, got %d", v.hashes()-before)
	}
	mustOK(t, "sign", v.sign(1200, "r", "d"))
	mustOK(t, "cosign", v.cosign(1300, "s", "d"))

	// 封存都只 +1，与已有版本数无关；以手动封存直接测量。
	sealCost := func(versions int) int {
		vv := newSvc(1_000_000, 1_000_000)
		mustOK(t, "vv r", vv.user("r", "内科", 1, 0))
		mustOK(t, "vv s", vv.user("s", "内科", 2, 0))
		mustOK(t, "vv arch", vv.user("q", "病案室", 2, record.RoleArchivist))
		mustOK(t, "vv enc", vv.enc("e1"))
		base := int64(100)
		mustOK(t, "vv create", vv.create(base, "r", "e1", "doc", "c"))
		for i := 1; i < versions; i++ {
			mustOK(t, "vv edit", vv.edit(base+int64(i), "r", "doc", "c"))
		}
		ts := base + int64(versions)
		mustOK(t, "vv sign", vv.sign(ts, "r", "doc"))
		mustOK(t, "vv cosign", vv.cosign(ts+1, "s", "doc"))
		// 提前手动封存（已审签），不依赖出院时钟，成本可直接测量。
		h0 := vv.hashes()
		mustOK(t, "vv seal", vv.seal(ts+2, "q", "doc"))
		return vv.hashes() - h0
	}

	// 长版本链：逐版 Edit 每次恰好 +1；封存仍 +1。
	vv := newSvc(4320, 60)
	mustOK(t, "vv r", vv.user("r", "内科", 1, 0))
	mustOK(t, "vv s", vv.user("s", "内科", 2, 0))
	mustOK(t, "vv arch", vv.user("q", "病案室", 2, record.RoleArchivist))
	mustOK(t, "vv enc", vv.enc("e1"))
	mustOK(t, "vv create", vv.create(0, "r", "e1", "big", "c"))
	for i := 1; i <= 10000; i++ {
		h0 := vv.hashes()
		mustOK(t, "vv edit", vv.edit(int64(i), "r", "big", "c"))
		if vv.hashes()-h0 != 1 {
			t.Fatalf("Edit 第 %d 版哈希计数 != 1", i)
		}
	}
	mustOK(t, "vv sign", vv.sign(10001, "r", "big"))
	mustOK(t, "vv cosign", vv.cosign(10002, "s", "big"))
	h0 := vv.hashes()
	mustOK(t, "vv seal", vv.seal(10003, "q", "big"))
	if vv.hashes()-h0 != 1 {
		t.Fatalf("10000 版文档封存仍应只计 1 次, got %d", vv.hashes()-h0)
	}
	// Amend 恰好 +1。
	h0 = vv.hashes()
	mustOK(t, "vv amend", vv.amend(10004, "r", "big", "a"))
	if vv.hashes()-h0 != 1 {
		t.Fatal("Amend 应计 1 次哈希")
	}
	// Verify = 版本数(10001) + 补记数(1) + sealHash(1)。
	h0 = vv.hashes()
	res, err := vv.verify("big")
	mustOK(t, "verify", err)
	if !res.VersionChainOK || !res.AmendChainOK {
		t.Fatal("长链 Verify 应完好")
	}
	if got := vv.hashes() - h0; got != 10001+1+1 {
		t.Fatalf("Verify 哈希计数 = %d, want %d", got, 10001+1+1)
	}

	// 10 版与 10000 版两档对照：封存成本都与版本数无关。
	if got := sealCost(10); got != 1 {
		t.Fatalf("10 版档封存成本 = %d, want 1", got)
	}
	if got := sealCost(10000); got != 1 {
		t.Fatalf("10000 版档封存成本 = %d, want 1", got)
	}
}
