package speq

import (
	"errors"
	"testing"
)

func TestSealExtensionAndMinGuarantee(t *testing.T) {
	s := testSystem(t)
	d0 := ord(2020, 1, 1)
	exp, _ := s.RegisterDevice(d0, "B1", "BOILER", d0) // exp=2021-01-01

	// 封存 30 天：到期日顺延 30 天（封存当日与启封当日按相差天数计）。
	sealD := ord(2020, 6, 1)
	must(t, s.Seal(sealD, "B1"))
	newExp, err := s.Unseal(sealD+30, "B1")
	must(t, err)
	if newExp != exp+30 {
		t.Fatalf("unseal extension = %d want %d", newExp, exp+30)
	}

	// 保障天数不足：剩余 5 天 < MinUnsealDays(10)，拒绝启封。
	first2 := ord(2020, 1, 2)
	var err2 error
	var exp2 int
	exp2, err2 = s.RegisterDevice(sealD+30, "B2", "BOILER", first2)
	must(t, err2)
	seal2 := exp2 - 4 // 在到期前 4 天封存
	must(t, s.Seal(seal2, "B2"))
	_, err = s.Unseal(seal2, "B2") // 同日启封，剩余 4 天 < 最小保障 10 天
	if codeOf(err) != ErrConditionNotMet {
		t.Fatalf("min guarantee should reject, got %v", err)
	}
	snap, _ := s.Get("B2")
	if !snap.Sealed || snap.Expiry != exp2 {
		t.Fatal("rejected unseal must not change state")
	}

	// 在封存状态下检验合格（已临近到期，按检验日为基准 +12 月），
	// 之后启封只顺延检验之后经过的封存天数。
	inspD := seal2
	inspectedExp, err := s.Inspect(inspD, "B2", ResultPass, 0)
	must(t, err)
	if inspectedExp != ord(2022, 1, 2) { // 落在提前窗口内：以原到期日 +12 月
		t.Fatalf("in-seal inspect base date, got %d want %d", inspectedExp, ord(2022, 1, 2))
	}
	// 检验后再过 3 天启封：只顺延 3 天。
	finalExp, err := s.Unseal(inspD+3, "B2")
	must(t, err)
	if finalExp != inspectedExp+3 {
		t.Fatalf("post-inspection unseal shifts only elapsed days, got %d want %d",
			finalExp, inspectedExp+3)
	}
}

func TestSealPrerequisites(t *testing.T) {
	s := testSystem(t)
	d0 := ord(2020, 1, 1)
	_, _ = s.RegisterDevice(d0, "B1", "BOILER", d0)
	// 超期对象不能封存。
	err := s.Seal(ord(2021, 1, 5), "B1")
	if codeOf(err) != ErrConditionNotMet {
		t.Fatalf("expired seal should be condition error, got %v", err)
	}
	// 不合格停用不能封存。
	s2 := testSystem(t)
	_, _ = s2.RegisterDevice(d0, "B1", "BOILER", d0)
	_, _ = s2.Inspect(ord(2020, 5, 1), "B1", ResultFail, 0)
	if err := s2.Seal(ord(2020, 5, 2), "B1"); codeOf(err) != ErrIllegalState {
		t.Fatalf("disabled seal should be state error, got %v", err)
	}
	// 封存期间不可使用。
	s3 := testSystem(t)
	_, _ = s3.RegisterDevice(d0, "B1", "BOILER", d0)
	mountSV(t, s3, d0, "V1", "B1")
	must(t, s3.Seal(ord(2020, 3, 1), "B1"))
	err = s3.RegisterUse(ord(2020, 3, 2), "B1")
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Reason != RejectDeviceSealed {
		t.Fatalf("sealed device unusable, got %v", err)
	}
}

func TestAttachmentTransferAndUsability(t *testing.T) {
	s := testSystem(t)
	d0 := ord(2020, 1, 1)
	_, _ = s.RegisterDevice(d0, "D1", "BOILER", d0)
	_, _ = s.RegisterDevice(d0, "D2", "BOILER", d0)
	// 无安全阀 -> 不可使用。
	err := s.RegisterUse(ord(2020, 2, 1), "D1")
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Reason != RejectNoSafetyValve {
		t.Fatalf("no safety valve, got %v", err)
	}
	// 一个安全阀挂 D1：D1 可用，D2 不可用。
	_, _ = s.RegisterAttachment(d0, "SV1", "SV", KindSafetyValve, d0)
	must(t, s.MountAttachment(ord(2020, 2, 1), "SV1", "D1"))
	must(t, s.RegisterUse(ord(2020, 2, 2), "D1"))
	err = s.RegisterUse(ord(2020, 2, 2), "D2")
	if !errors.As(err, &rej) || rej.Reason != RejectNoSafetyValve {
		t.Fatalf("D2 still lacks valve, got %v", err)
	}
	// 转移到 D2：D1 变为缺安全阀，D2 可用。
	must(t, s.MountAttachment(ord(2020, 3, 1), "SV1", "D2"))
	err = s.RegisterUse(ord(2020, 3, 2), "D1")
	if !errors.As(err, &rej) || rej.Reason != RejectNoSafetyValve {
		t.Fatalf("D1 lost valve, got %v", err)
	}
	must(t, s.RegisterUse(ord(2020, 3, 2), "D2"))

	// 压力表超期不影响"至少一个安全阀"，但会使设备不可用。
	_, _ = s.RegisterAttachment(ord(2020, 3, 2), "PG1", "PG", KindPressureGauge, ord(2020, 3, 2))
	must(t, s.MountAttachment(ord(2020, 3, 3), "PG1", "D2"))
	must(t, s.RegisterUse(ord(2020, 8, 1), "D2")) // PG 2020-09-02 到期前可用
	err = s.RegisterUse(ord(2020, 9, 5), "D2")
	if !errors.As(err, &rej) || rej.Reason != RejectAttachment || rej.Attachment != "PG1" {
		t.Fatalf("expired gauge should block device (smallest bad id), got %v", err)
	}

	// 转移资格：封存/停用/超期附件不得转移。
	must(t, s.Seal(ord(2020, 9, 6), "SV1"))
	if err := s.MountAttachment(ord(2020, 9, 7), "SV1", "D1"); codeOf(err) != ErrIllegalState {
		t.Fatalf("sealed attachment transfer = %v", err)
	}
}

func TestScrapDetach(t *testing.T) {
	s := testSystem(t)
	d0 := ord(2020, 1, 1)
	_, _ = s.RegisterDevice(d0, "D1", "BOILER", d0)
	_, _ = s.RegisterAttachment(d0, "SV1", "SV", KindSafetyValve, d0)
	_, _ = s.RegisterAttachment(d0, "PG1", "PG", KindPressureGauge, d0)
	must(t, s.MountAttachment(d0, "SV1", "D1"))
	must(t, s.MountAttachment(d0, "PG1", "D1"))

	// 附件报废：自动摘除。
	must(t, s.Scrap(ord(2020, 5, 1), "PG1"))
	if got := s.AttachmentsOf("D1"); len(got) != 1 || got[0] != "SV1" {
		t.Fatalf("scrapped attach should detach, got %v", got)
	}
	_, err := s.Inspect(ord(2020, 5, 2), "PG1", ResultPass, 0)
	if codeOf(err) != ErrScrapped {
		t.Fatalf("scrapped object rejects ops, got %v", err)
	}
	// 设备报废：剩余附件自动脱离。
	must(t, s.Scrap(ord(2020, 6, 1), "D1"))
	snap, _ := s.Get("SV1")
	if snap.Host != "" {
		t.Fatalf("device scrap should detach attachments, host=%q", snap.Host)
	}
}

func TestErrorOrdering(t *testing.T) {
	s := testSystem(t)
	d0 := ord(2020, 1, 1)
	_, _ = s.RegisterDevice(d0, "B1", "BOILER", d0)
	// 参数非法优先于日期回退。
	_, err := s.Inspect(-5, "B1", ResultPass, 0)
	if codeOf(err) != ErrInvalidParameter {
		t.Fatalf("invalid > regression, got %v", err)
	}
	// 日期回退优先于对象不存在。
	_, err = s.Inspect(d0-10, "GHOST", ResultPass, 0)
	if codeOf(err) != ErrDateRegression {
		t.Fatalf("regression > notfound, got %v", err)
	}
	// 推进日期后对象不存在。
	_, err = s.Inspect(d0, "GHOST", ResultPass, 0)
	if codeOf(err) != ErrNotFound {
		t.Fatalf("notfound, got %v", err)
	}
	// 已报废优先于状态/条件。
	must(t, s.Scrap(ord(2020, 2, 1), "B1"))
	if err := s.Seal(ord(2020, 2, 2), "B1"); codeOf(err) != ErrScrapped {
		t.Fatalf("scrapped > state, got %v", err)
	}
	// 被拒绝操作不推进日期：再用 2020-02-02 操作一个不存在对象，
	// 应得到"对象不存在"而非日期回退。
	_, err = s.Inspect(ord(2020, 2, 2), "X9", ResultPass, 0)
	if codeOf(err) != ErrNotFound {
		t.Fatalf("rejected op must not advance date, got %v", err)
	}
}

func TestDateMonotonicAcceptedOnly(t *testing.T) {
	s := testSystem(t)
	d0 := ord(2020, 1, 1)
	_, _ = s.RegisterDevice(d0, "B1", "BOILER", d0)
	// 失败的使用登记不得推进日期。
	_ = s.RegisterUse(ord(2020, 5, 1), "B1") // 因缺安全阀拒绝
	if s.LastDate() != d0 {
		t.Fatalf("rejected use must not advance date, got %d", s.LastDate())
	}
}
