package booking

import (
	"testing"

	"ontology/slotpool"
)

// TestTableRules 合并的表驱动规则用例：
// 释放点取等、借用后退号归属、签到窗口两端、迟退记爽约、爽约时刻取 start+G、
// 候补递补与作废、禁约窗口取等、拒绝次序、被拒不落地。
func TestTableRules(t *testing.T) {
	type step struct {
		act     string
		now     int64
		p, slot string
		ch      slotpool.Channel
		start   int64
		cap, on int
		want    error
		wantSeq int64
		checkUO int
		checkUS int
	}
	cases := []struct {
		name             string
		r, e, g, c, k, w int
		steps            []step
	}{
		{
			name: "addslot validation and reject order",
			steps: []step{
				{act: "add", now: 10, slot: "", start: 100, cap: 1, want: ErrInvalid},
				{act: "add", now: 10, slot: "s", start: 10, cap: 1, want: ErrInvalid},
				{act: "add", now: 10, slot: "s", start: 100, cap: 0, want: ErrInvalid},
				{act: "add", now: 10, slot: "s", start: 100, cap: 1001, want: ErrInvalid},
				{act: "add", now: 10, slot: "s", start: 100, cap: 5, on: 6, want: ErrInvalid},
				{act: "add", now: 10, slot: "s", start: 100, cap: 1, want: nil, wantSeq: 0},
				{act: "add", now: 11, slot: "s", start: 100, cap: 1, want: ErrInvalid},
				{act: "book", now: 9, p: "P", slot: "s", ch: slotpool.Online, want: ErrClockBack},
				{act: "book", now: 12, p: "P", slot: "nope", ch: slotpool.Online, want: ErrSlotMissing},
				{act: "book", now: 12, slot: "s", want: ErrInvalid},
				{act: "book", now: 100, p: "P", slot: "s", ch: slotpool.Online, want: ErrOpen},
				{act: "checkin", now: 101, p: "P", slot: "zzz", want: ErrSlotMissing},
				{act: "cancel", now: 102, p: "P", slot: "zzz", want: ErrSlotMissing},
				{act: "wait", now: 103, p: "P", slot: "zzz", want: ErrSlotMissing},
			},
		},
		{
			name: "release point equality online quota joins pool",
			r:    60,
			steps: []step{
				{act: "add", now: 0, slot: "s", start: 600, cap: 3, on: 2},
				{act: "book", now: 539, p: "A", slot: "s", ch: slotpool.Online},
				{act: "book", now: 539, p: "B", slot: "s", ch: slotpool.Online},
				{act: "book", now: 539, p: "D", slot: "s", ch: slotpool.OnSite},
				{act: "book", now: 539, p: "F", slot: "s", ch: slotpool.OnSite, want: ErrNoQuota},
				{act: "cancel", now: 539, p: "A", slot: "s", checkUO: 1, checkUS: 1},
				{act: "book", now: 539, p: "F", slot: "s", ch: slotpool.OnSite, want: ErrNoQuota},
				{act: "book", now: 540, p: "F", slot: "s", ch: slotpool.OnSite, checkUO: 1, checkUS: 2},
			},
		},
		{
			name: "borrowed quota returns to original channel",
			r:    60,
			steps: []step{
				{act: "add", now: 0, slot: "s", start: 600, cap: 2, on: 1},
				{act: "book", now: 539, p: "A", slot: "s", ch: slotpool.Online},
				{act: "book", now: 540, p: "D", slot: "s", ch: slotpool.OnSite},
				{act: "cancel", now: 541, p: "D", slot: "s", checkUO: 1, checkUS: 0},
				{act: "book", now: 542, p: "E", slot: "s", ch: slotpool.OnSite, checkUO: 1, checkUS: 1},
				{act: "cancel", now: 543, p: "A", slot: "s", checkUO: 0, checkUS: 1},
			},
		},
		{
			name: "checkin window both ends inclusive",
			e:    30, g: 10,
			steps: []step{
				{act: "add", now: 0, slot: "s", start: 600, cap: 3, on: 3},
				{act: "book", now: 100, p: "A", slot: "s", ch: slotpool.Online},
				{act: "book", now: 100, p: "B", slot: "s", ch: slotpool.Online},
				{act: "book", now: 100, p: "C", slot: "s", ch: slotpool.Online},
				{act: "checkin", now: 569, p: "A", slot: "s", want: ErrTooEarly},
				{act: "checkin", now: 570, p: "A", slot: "s"},
				{act: "checkin", now: 610, p: "B", slot: "s"},
				{act: "checkin", now: 611, p: "C", slot: "s", want: ErrNoBooking},
				{act: "checkin", now: 611, p: "Z", slot: "s", want: ErrNoBooking},
			},
		},
		{
			name: "late cancel records noshow",
			r:    60, c: 1, k: 1, w: 100000,
			steps: []step{
				{act: "add", now: 0, slot: "s", start: 700, cap: 3, on: 3},
				{act: "book", now: 100, p: "A", slot: "s", ch: slotpool.Online},
				{act: "book", now: 100, p: "Q", slot: "s", ch: slotpool.Online},
				{act: "cancel", now: 699, p: "Q", slot: "s"},                      // 恰等 start-C 免责
				{act: "cancel", now: 700, p: "A", slot: "s", want: ErrLateCancel}, // 开诊后仍可退，严格迟退记 700
				{act: "add", now: 700, slot: "s2", start: 9000, cap: 3, on: 2},
				{act: "book", now: 700, p: "A", slot: "s2", ch: slotpool.Online, want: ErrBanned},
				{act: "book", now: 700, p: "A", slot: "s2", ch: slotpool.OnSite}, // 现场不受限
				{act: "checkin", now: 9000, p: "A", slot: "s2"},
				{act: "cancel", now: 9000, p: "A", slot: "s2", want: ErrAlreadyIn},
			},
		},
		{
			name: "noshow stamped at start+g",
			g:    10, k: 1, w: 41,
			steps: []step{
				{act: "add", now: 0, slot: "s", start: 600, cap: 1, on: 1},
				{act: "book", now: 100, p: "A", slot: "s", ch: slotpool.Online},
				// 651 落地（dead=610）；691-610=81>=41 已出窗，线上可订。
				// 若错误地把时刻记成 651，则 691-651=40<41 应被禁约。
				{act: "checkin", now: 651, p: "A", slot: "s", want: ErrNoBooking},
				{act: "add", now: 651, slot: "s2", start: 5000, cap: 1, on: 1},
				{act: "book", now: 691, p: "A", slot: "s2", ch: slotpool.Online},
			},
		},
		{
			name: "waitlist promote then checked in after start",
			r:    60, g: 10,
			steps: []step{
				{act: "add", now: 0, slot: "s", start: 600, cap: 1, on: 1},
				{act: "book", now: 100, p: "A", slot: "s", ch: slotpool.Online},
				{act: "wait", now: 500, p: "Q", slot: "s", want: ErrNotWaitable},
				{act: "wait", now: 540, p: "H", slot: "s"},
				{act: "wait", now: 541, p: "H", slot: "s", want: ErrDuplicate},
				{act: "checkin", now: 611, p: "H", slot: "s"}, // 落地 A→H 递补、已签到
			},
		},
		{
			name: "waitlist promoted by cancel before start",
			r:    60, e: 30,
			steps: []step{
				{act: "add", now: 0, slot: "s", start: 600, cap: 1, on: 1},
				{act: "book", now: 100, p: "A", slot: "s", ch: slotpool.Online},
				{act: "wait", now: 540, p: "H", slot: "s"},
				{act: "cancel", now: 541, p: "A", slot: "s"},
				{act: "checkin", now: 570, p: "H", slot: "s"},
			},
		},
		{
			name: "waitlist void when slot dead and full",
			r:    60, e: 30, g: 10,
			steps: []step{
				{act: "add", now: 0, slot: "s", start: 600, cap: 2, on: 2},
				{act: "book", now: 100, p: "A", slot: "s", ch: slotpool.Online},
				{act: "book", now: 100, p: "B", slot: "s", ch: slotpool.Online},
				{act: "checkin", now: 590, p: "A", slot: "s"},
				{act: "wait", now: 595, p: "H", slot: "s"},
				{act: "wait", now: 596, p: "I", slot: "s"},
				// 611：B 落地，H 递补为已签到；该槽落地完毕，I 作废，不记爽约。
				{act: "checkin", now: 611, p: "H", slot: "s"},
				{act: "checkin", now: 611, p: "I", slot: "s", want: ErrNoBooking},
			},
		},
		{
			name: "rejected op never lands nor advances",
			g:    10,
			steps: []step{
				{act: "add", now: 0, slot: "s", start: 600, cap: 1, on: 1},
				{act: "book", now: 100, p: "A", slot: "s", ch: slotpool.Online},
				{act: "checkin", now: 611, p: "A", slot: "zzz", want: ErrSlotMissing},
				{act: "checkin", now: 610, p: "A", slot: "s"}, // 仍恰等可签到
				{act: "book", now: 609, p: "X", slot: "s", ch: slotpool.Online, want: ErrClockBack},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, w := tc.k, tc.w
			if k == 0 {
				k, w = 2, 10000
			}
			s := New(tc.r, tc.e, tc.g, tc.c, k, w)
			seq := int64(0)
			for i, st := range tc.steps {
				var err error
				switch st.act {
				case "add":
					err = s.AddSlot(st.now, st.slot, st.start, st.cap, st.on)
				case "book":
					var got int64
					got, err = s.Book(st.now, pb(st.p), st.slot, st.ch)
					if err == nil {
						seq++
						if got != seq {
							t.Fatalf("step %d: seq=%d want %d", i, got, seq)
						}
					}
				case "checkin":
					err = s.CheckIn(st.now, pb(st.p), st.slot)
				case "cancel":
					err = s.Cancel(st.now, pb(st.p), st.slot)
				case "wait":
					err = s.JoinWait(st.now, pb(st.p), st.slot)
				}
				if !errorIs(err, st.want) {
					t.Fatalf("step %d %s: got %v want %v", i, st.act, err, st.want)
				}
				if st.checkUO != 0 || st.checkUS != 0 {
					sp := s.pool.Get(st.slot)
					if sp.UO != st.checkUO || sp.US != st.checkUS {
						t.Fatalf("step %d counts uo/us = %d/%d want %d/%d",
							i, sp.UO, sp.US, st.checkUO, st.checkUS)
					}
				}
			}
		})
	}
}

func errorIs(got, want error) bool {
	if want == nil {
		return got == nil
	}
	return got == want
}
