package offline

import (
	"errors"
	"testing"

	"ontology/playback"
)

func errCode(err error) string {
	if err == nil {
		return ""
	}
	var ee *ExpiredError
	if errors.As(err, &ee) {
		return "expired:" + ee.Reason.String()
	}
	return err.Error()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want %v, got %v", target, err)
	}
}

func wantExpired(t *testing.T, err error, reason playback.Reason) *ExpiredError {
	t.Helper()
	var ee *ExpiredError
	if !errors.As(err, &ee) {
		t.Fatalf("want ExpiredError, got %v", err)
	}
	if ee.Reason != reason {
		t.Fatalf("want reason %v, got %v", reason, ee.Reason)
	}
	return ee
}

func TestCooldownAndSlots(t *testing.T) {
	t.Run("exact_release_and_full", func(t *testing.T) {
		m := NewManager(2, 100, 1000, 50, 10)
		must(t, m.AddAccount(0, "a"))
		must(t, m.Register(0, "a", "d1"))
		must(t, m.Register(0, "a", "d2"))
		must(t, m.Deregister(10, "a", "d1"))
		wantIs(t, m.Register(20, "a", "d3"), ErrDeviceFull)
		wantIs(t, m.Register(109, "a", "d3"), ErrDeviceFull)
		must(t, m.Register(110, "a", "d3")) // 恰等 10+100 释放
	})

	t.Run("reuse_own_cooldown_slot", func(t *testing.T) {
		m := NewManager(2, 100, 1000, 50, 10)
		must(t, m.AddAccount(0, "a"))
		must(t, m.Register(0, "a", "d1"))
		must(t, m.Register(0, "a", "d2"))
		must(t, m.Deregister(10, "a", "d1"))
		must(t, m.Register(20, "a", "d1")) // 复用自身冷却名额
		wantIs(t, m.Register(20, "a", "d3"), ErrDeviceFull)
	})

	t.Run("deregister_deletes_licenses_no_restore", func(t *testing.T) {
		m := NewManager(2, 100, 1000, 50, 10)
		must(t, m.AddAccount(0, "a"))
		must(t, m.AddTitle(0, "T", 2000))
		must(t, m.Register(0, "a", "d1"))
		must(t, m.Download(100, "a", "d1", "T"))
		must(t, m.Deregister(110, "a", "d1"))
		must(t, m.Register(220, "a", "d1"))
		wantIs(t, m.Play(220, "a", "d1", "T"), ErrNoLicense)
	})

	t.Run("cooldown_zero_is_immediately_released", func(t *testing.T) {
		m := NewManager(1, 5, 1000, 50, 10) // cool=5：4+5=9
		must(t, m.AddAccount(0, "a"))
		must(t, m.Register(0, "a", "d1"))
		must(t, m.Deregister(4, "a", "d1"))
		wantIs(t, m.Register(8, "a", "d2"), ErrDeviceFull) // now=8 < 9 仍占名额
		must(t, m.Register(9, "a", "d2"))                  // 恰等释放
	})
}

func TestLicenseAndPlaybackPhases(t *testing.T) {
	t.Run("start_before_rental_end_play_crosses_rental_end", func(t *testing.T) {
		m := NewManager(2, 100, 1000, 50, 10)
		must(t, m.AddAccount(0, "a"))
		must(t, m.AddTitle(0, "T", 2000))
		must(t, m.Register(0, "a", "d2"))
		must(t, m.Download(200, "a", "d2", "T")) // rentalEnd=1200
		must(t, m.Play(1199, "a", "d2", "T"))    // firstPlay=1199
		info, err := m.Status("a", "d2", "T", 1200)
		if err != nil || info.State != playback.StatePlaying || info.Exp != 1249 {
			t.Fatalf("cross rental end: info=%+v err=%v", info, err)
		}
		must(t, m.Play(1248, "a", "d2", "T"))
		ee := wantExpired(t, m.Play(1249, "a", "d2", "T"), playback.ReasonPlaybackEnded)
		if ee.Exp != 1249 {
			t.Fatalf("exp want 1249, got %d", ee.Exp)
		}
	})

	t.Run("never_play_rental_end_exact", func(t *testing.T) {
		m := NewManager(2, 100, 1000, 50, 10)
		must(t, m.AddAccount(0, "a"))
		must(t, m.AddTitle(0, "T", 2000))
		must(t, m.Register(0, "a", "d2"))
		must(t, m.Download(200, "a", "d2", "T"))
		wantExpired(t, m.Play(1200, "a", "d2", "T"), playback.ReasonRentalEnded)
	})

	t.Run("renew_unplayed_reject_when_playing", func(t *testing.T) {
		m := NewManager(2, 100, 1000, 50, 1)
		must(t, m.AddAccount(0, "a"))
		must(t, m.AddTitle(0, "T", 5000))
		must(t, m.Register(0, "a", "d"))
		must(t, m.Download(200, "a", "d", "T"))
		must(t, m.Download(1190, "a", "d", "T"))
		info, _ := m.Status("a", "d", "T", 1190)
		if info.State != playback.StateNotPlayed || info.Exp != 2190 {
			t.Fatalf("renew: %+v", info)
		}
		must(t, m.Play(1191, "a", "d", "T"))
		wantIs(t, m.Download(1192, "a", "d", "T"), ErrAlreadyPlaying)
	})

	t.Run("redownload_expired_replaces_record", func(t *testing.T) {
		m := NewManager(2, 100, 100, 50, 1)
		must(t, m.AddAccount(0, "a"))
		must(t, m.AddTitle(0, "T", 10000))
		must(t, m.Register(0, "a", "d"))
		must(t, m.Download(0, "a", "d", "T"))
		must(t, m.Download(100, "a", "d", "T")) // 恰等过期后重新签发
		info, _ := m.Status("a", "d", "T", 150)
		if info.State != playback.StateNotPlayed || info.Exp != 200 {
			t.Fatalf("re-issue: %+v", info)
		}
	})

	t.Run("omax_counts_only_valid", func(t *testing.T) {
		m := NewManager(2, 100, 1000, 50, 1)
		must(t, m.AddAccount(0, "a"))
		must(t, m.AddTitle(0, "T", 2000))
		must(t, m.AddTitle(0, "U", 2000))
		must(t, m.Register(0, "a", "d"))
		must(t, m.Download(200, "a", "d", "T"))
		wantIs(t, m.Download(300, "a", "d", "U"), ErrLicenseFull)
		must(t, m.Download(1200, "a", "d", "U")) // T 已过期，腾出有效名额
		wantIs(t, m.Download(1201, "a", "d", "T"), ErrLicenseFull)
	})
}

func TestTitleEndChangesAndReasonOrder(t *testing.T) {
	m := NewManager(2, 100, 1000, 50, 10)
	must(t, m.AddAccount(0, "a"))
	must(t, m.AddTitle(0, "T", 2000))
	must(t, m.Register(0, "a", "d"))
	must(t, m.Download(200, "a", "d", "T"))

	// 提前下架：有效许可立即仅因下架过期。
	// 提前下架：end 必须严格大于操作 now（此处仍远早于当前许可有效期）。
	must(t, m.SetTitleEnd(300, "T", 400))
	wantExpired(t, m.Play(400, "a", "d", "T"), playback.ReasonTitleOffShelf)

	// 延后下架：仅因下架过期的未开播许可复活（rentalEnd=1200 仍在未来）。
	must(t, m.SetTitleEnd(450, "T", 900))
	must(t, m.Play(450, "a", "d", "T")) // firstPlay=450, 播放期至 500

	// 播放期满后即使下架时刻延后也不复活。
	must(t, m.SetTitleEnd(500, "T", 5000))
	wantExpired(t, m.Play(500, "a", "d", "T"), playback.ReasonPlaybackEnded)
	must(t, m.SetTitleEnd(501, "T", 6000))
	wantExpired(t, m.Play(501, "a", "d", "T"), playback.ReasonPlaybackEnded)

	// 原因次序：未开播且 now>=titleEnd 时，即便 rentalEnd 也已过仍先报下架。
	m2 := NewManager(2, 100, 1000, 50, 10)
	must(t, m2.AddAccount(0, "a"))
	must(t, m2.AddTitle(0, "U", 500))
	must(t, m2.Register(0, "a", "d"))
	must(t, m2.Download(0, "a", "d", "U")) // rentalEnd=1000, titleEnd=500
	wantExpired(t, m2.Play(600, "a", "d", "U"), playback.ReasonTitleOffShelf)

	// 下架在 Download 拒绝次序中先于“已开播不可续”。
	m3 := NewManager(2, 100, 1000, 50, 1)
	must(t, m3.AddAccount(0, "a"))
	must(t, m3.AddTitle(0, "V", 200))
	must(t, m3.Register(0, "a", "d"))
	must(t, m3.Download(0, "a", "d", "V"))
	must(t, m3.Play(50, "a", "d", "V"))
	wantIs(t, m3.Download(200, "a", "d", "V"), ErrTitleOffShelf)
}

func TestRejectionOrderAndNoStateChange(t *testing.T) {
	m := NewManager(2, 100, 1000, 50, 10)
	must(t, m.AddAccount(10, "a"))

	wantIs(t, m.Register(5, "", "d"), ErrInvalidArg)
	wantIs(t, m.Register(5, "b", "d"), ErrClockRollback)
	wantIs(t, m.Register(10, "b", "d"), ErrAccountNotFound)
	must(t, m.Register(10, "a", "d"))
	wantIs(t, m.Register(10, "a", "d"), ErrDeviceRegistered)

	// 被拒不改时钟：now=9 仍报时钟回退。
	wantIs(t, m.Deregister(9, "a", "d"), ErrClockRollback)

	// Download 拒绝次序。
	wantIs(t, m.Download(9, "a", "d", "T"), ErrClockRollback)
	wantIs(t, m.Download(10, "a", "d", "T"), ErrTitleNotFound)
	must(t, m.AddTitle(10, "T", 20))
	wantIs(t, m.Download(10, "a", "x", "T"), ErrDeviceNotFound)
	wantIs(t, m.Download(20, "a", "d", "T"), ErrTitleOffShelf)

	// Play 拒绝次序。
	wantIs(t, m.Play(9, "a", "d", "T"), ErrClockRollback)
	wantIs(t, m.Play(10, "a", "x", "T"), ErrDeviceNotFound)
	wantIs(t, m.Play(10, "a", "d", "T"), ErrNoLicense)

	// Deregister 拒绝次序。
	wantIs(t, m.Deregister(9, "a", "x"), ErrClockRollback)
	wantIs(t, m.Deregister(10, "a", "x"), ErrDeviceNotFound)

	// 登记类重复。
	wantIs(t, m.AddAccount(10, "a"), ErrAccountExists)
	wantIs(t, m.AddTitle(10, "T", 99), ErrTitleExists)

	// SetTitleEnd 参数与存在性。
	wantIs(t, m.SetTitleEnd(10, "T", 5), ErrInvalidArg) // end<=now
	wantIs(t, m.SetTitleEnd(10, "Z", 99), ErrTitleNotFound)

	// 构造参数非法 panic。
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expect panic on invalid constructor")
			}
		}()
		NewManager(0, 1, 1, 1, 1)
	}()
}
