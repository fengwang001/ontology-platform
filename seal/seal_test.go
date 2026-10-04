package seal_test

import (
	"encoding/hex"
	"errors"
	"sync"
	"testing"

	"ontology/record"
	"ontology/seal"
	"ontology/sign"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// 题述第一例：d1 完整流转，d2 在封存时刻被封存并入/出缺陷清单。
func TestWorkedExampleOne(t *testing.T) {
	s := record.New(4320, 60)
	must(t, s.AddUser("r", "内科", 1, nil))
	must(t, s.AddUser("s", "内科", 2, nil))
	must(t, s.AddUser("k", "外科", 3, nil))
	must(t, s.OpenEnc("e"))
	must(t, s.Discharge(1000, "e"))

	must(t, s.Create(1100, "r", "e", "d1", "a"))
	must(t, sign.Sign(s, 1200, "r", "d1"))
	must(t, s.Edit(1300, "r", "d1", "b"))
	di, _ := s.DocInfo("d1")
	if di.Status != record.StatusDraft || di.VersionCount != 2 {
		t.Fatalf("edit after sign: %+v", di)
	}
	must(t, sign.Sign(s, 1400, "r", "d1"))
	must(t, sign.Cosign(s, 1500, "s", "d1"))
	if err := s.Edit(1600, "r", "d1", "c"); !errors.Is(err, record.ErrState) {
		t.Fatalf("r edit cosigned = %v want state", err)
	}
	if err := s.Edit(1610, "k", "d1", "c"); !errors.Is(err, record.ErrPermission) {
		t.Fatalf("k edit = %v want permission", err)
	}

	must(t, s.Create(1700, "r", "e", "d2", "x"))
	// 5319 尚未封存，可编辑。
	must(t, s.Edit(5319, "r", "d2", "x2"))
	di, _ = s.DocInfo("d2")
	if di.Sealed {
		t.Fatal("d2 sealed before 5320")
	}
	// 5320 取等：对草稿 d2 的迟签是可接受操作，其开头把 d1、d2 一并封存。
	must(t, sign.Sign(s, 5320, "r", "d2"))
	d1, _ := s.DocInfo("d1")
	d2, _ := s.DocInfo("d2")
	if !d1.Sealed || !d2.Sealed || d1.Gen != 1 || d2.Gen != 1 {
		t.Fatalf("auto seal: d1=%+v d2=%+v", d1, d2)
	}
	if d2.VersionCount != 2 {
		t.Fatalf("d2 versions=%d want 2", d2.VersionCount)
	}
	// 封存后 Edit 状态不符。
	if err := s.Edit(5321, "r", "d2", "x4"); !errors.Is(err, record.ErrState) {
		t.Fatalf("edit sealed = %v", err)
	}
	// 迟审签。
	must(t, sign.Cosign(s, 5323, "s", "d2"))
	defs, err := s.Defects(5323)
	must(t, err)
	if len(defs) != 0 {
		t.Fatalf("defects=%v want empty", defs)
	}
	// Amend：r 成功、k 无权限。
	must(t, seal.Amend(s, 5324, "r", "d1", "补记"))
	if err := seal.Amend(s, 5325, "k", "d1", "补记"); !errors.Is(err, record.ErrPermission) {
		t.Fatalf("k amend = %v want permission", err)
	}
	v, err := seal.Verify(s, "d1")
	must(t, err)
	if !v.VersionChainOK || !v.AmendChainOK || v.Gen != 1 || v.SealHash == nil {
		t.Fatalf("verify: %+v", v)
	}
	// 补记不改变 sealHash。
	if hex.EncodeToString(v.SealHash) != hex.EncodeToString(d1.SealHash) {
		t.Fatal("sealHash changed by amend")
	}
}

// 题述第二例：解封窗口取等、gen 递增、sealHash 改变而版本链不变。
func TestUnsealWindow(t *testing.T) {
	s := record.New(4320, 60)
	must(t, s.AddUser("r", "内科", 1, nil))
	must(t, s.AddUser("s", "内科", 2, nil))
	must(t, s.AddUser("a", "院办", 3, []string{record.RoleMedical}))
	must(t, s.AddUser("b", "病案室", 2, []string{record.RoleRecords}))
	must(t, s.AddUser("ab", "院办", 3, []string{record.RoleMedical, record.RoleRecords}))
	must(t, s.OpenEnc("e"))
	must(t, s.Discharge(1000, "e"))
	must(t, s.Create(1100, "r", "e", "d1", "a"))
	must(t, sign.Sign(s, 1200, "r", "d1"))
	must(t, sign.Cosign(s, 1300, "s", "d1"))
	must(t, s.Create(2000, "r", "e", "d2", "x"))
	must(t, sign.Sign(s, 5320, "r", "d2")) // 5320 自动封存 d1 与 d2（d2 迟签）
	d1, _ := s.DocInfo("d1")
	if !d1.Sealed || d1.Gen != 1 {
		t.Fatalf("d1 not sealed at 5320: %+v", d1)
	}
	sealHash1 := d1.SealHash

	// 同一人兼两权也不行。
	if err := seal.Unseal(s, 6000, "ab", "ab", "d1"); !errors.Is(err, record.ErrPermission) {
		t.Fatalf("same person unseal = %v want permission", err)
	}
	// b 缺医务权、a 缺病案权。
	if err := seal.Unseal(s, 6000, "b", "b", "d1"); !errors.Is(err, record.ErrPermission) {
		t.Fatalf("b,b unseal = %v", err)
	}
	must(t, seal.Unseal(s, 6000, "a", "b", "d1"))
	d1, _ = s.DocInfo("d1")
	if !d1.Sealed || !d1.WindowOpen || d1.UnsealEnd != 6060 {
		t.Fatalf("window: %+v", d1)
	}
	// 窗口内不可 Amend、不可再次 Unseal。
	if err := seal.Amend(s, 6010, "r", "d1", "x"); !errors.Is(err, record.ErrState) {
		t.Fatalf("amend in window = %v", err)
	}
	if err := seal.Unseal(s, 6010, "a", "b", "d1"); !errors.Is(err, record.ErrState) {
		t.Fatalf("re-unseal = %v", err)
	}
	// 6059 已审签文档 Edit 报状态不符。
	if err := s.Edit(6059, "r", "d1", "c"); !errors.Is(err, record.ErrState) {
		t.Fatalf("edit cosigned in window = %v want state", err)
	}
	// 6060 取等：操作开头再封存。
	must(t, seal.Amend(s, 6060, "r", "d1", "re-seal touch"))
	d1, _ = s.DocInfo("d1")
	if !d1.Sealed || d1.Gen != 2 || d1.WindowOpen {
		t.Fatalf("reseal: %+v", d1)
	}
	if hex.EncodeToString(d1.SealHash) == hex.EncodeToString(sealHash1) {
		t.Fatal("sealHash identical across generations")
	}
	if hex.EncodeToString(d1.SealHash) != "4a884d370901d6fa30942d244651ef89cb6fee9b8156554460d14086c5b3a4e9" {
		t.Fatalf("gen2 sealHash=%s", hex.EncodeToString(d1.SealHash))
	}
	if d1.VersionCount != 1 {
		t.Fatalf("version chain grew during sealed period: %d", d1.VersionCount)
	}
}

func TestSealBoundaryEquality(t *testing.T) {
	s := record.New(4320, 60)
	must(t, s.AddUser("r", "内科", 2, []string{record.RoleArchivist}))
	must(t, s.OpenEnc("e"))
	must(t, s.Discharge(1000, "e"))
	must(t, s.Create(1100, "r", "e", "d", "a"))
	must(t, sign.Sign(s, 1200, "r", "d"))
	// dis+T=5320 恰等即封存：手动 Seal 在 5319 可成功（已审签）。
	must(t, seal.Seal(s, 5319, "r", "d"))
	di, _ := s.DocInfo("d")
	if !di.Sealed || di.Gen != 1 {
		t.Fatalf("manual seal: %+v", di)
	}
	// 已封存手动再封存报状态不符。
	if err := seal.Seal(s, 5320, "r", "d"); !errors.Is(err, record.ErrState) {
		t.Fatalf("re-seal = %v", err)
	}
}

func TestManualSealRequiresArchiverAndCosigned(t *testing.T) {
	s := record.New(4320, 60)
	must(t, s.AddUser("r", "内科", 1, nil))
	must(t, s.AddUser("s", "内科", 2, []string{record.RoleArchivist}))
	must(t, s.OpenEnc("e"))
	must(t, s.Create(100, "r", "e", "d", "a"))
	// 非归档员无权限（先于状态判定）。
	if err := seal.Seal(s, 200, "r", "d"); !errors.Is(err, record.ErrPermission) {
		t.Fatalf("non-archiver = %v want permission", err)
	}
	must(t, sign.Sign(s, 300, "r", "d"))
	// 仅已签未审签 -> 状态不符。
	if err := seal.Seal(s, 400, "s", "d"); !errors.Is(err, record.ErrState) {
		t.Fatalf("signed-only seal = %v want state", err)
	}
}

func TestCreateAfterSealTime(t *testing.T) {
	s := record.New(4320, 60)
	must(t, s.AddUser("r", "内科", 2, nil))
	must(t, s.OpenEnc("e"))
	must(t, s.Discharge(1000, "e"))
	if err := s.Create(5320, "r", "e", "d", "a"); !errors.Is(err, record.ErrState) {
		t.Fatalf("create at seal time = %v want state", err)
	}
}

// 并发调用等价于某一串行顺序：所有结果只能是成功或定义好的错误，最终状态自洽。
func TestConcurrentSmoke(t *testing.T) {
	s := record.New(4320, 60)
	must(t, s.AddUser("r", "内科", 1, nil))
	must(t, s.AddUser("s2", "内科", 2, []string{record.RoleArchivist}))
	must(t, s.OpenEnc("e"))
	must(t, s.Create(100, "r", "e", "d", "a"))
	var wg sync.WaitGroup
	try := func(f func() error) {
		defer wg.Done()
		_ = f()
	}
	for ts := int64(200); ts < 260; ts++ {
		wg.Add(4)
		go try(func() error { return sign.Sign(s, ts, "r", "d") })
		go try(func() error { return sign.Cosign(s, ts, "s2", "d") })
		go try(func() error { return s.Edit(ts, "r", "d", "c") })
		go try(func() error { return sign.Return(s, ts, "s2", "d") })
		wg.Wait()
	}
	v, err := seal.Verify(s, "d")
	must(t, err)
	if !v.VersionChainOK || !v.AmendChainOK {
		t.Fatalf("concurrent final state corrupt: %+v", v)
	}
}
