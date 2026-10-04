package usage

import (
	"reflect"
	"testing"
)

func TestActiveConsumers(t *testing.T) {
	const q int64 = 30
	cases := []struct {
		name string
		run  func(tk *Tracker)
		now  int64
		want []string
	}{
		{
			"strict boundary lastAccess==now-q is inactive",
			func(tk *Tracker) { tk.Record("c1", "d", 70) },
			100, nil,
		},
		{
			"lastAccess==now-q+1 is active",
			func(tk *Tracker) { tk.Record("c1", "d", 71) },
			100, []string{"c1"},
		},
		{
			"acked consumer is inactive",
			func(tk *Tracker) {
				tk.Record("c1", "d", 90)
				tk.Ack("c1", "d", 95)
			},
			100, nil,
		},
		{
			"successful access after ack reactivates",
			func(tk *Tracker) {
				tk.Record("c1", "d", 90)
				tk.Ack("c1", "d", 92)
				tk.Record("c1", "d", 95) // 确认后再成功访问：确认作废
			},
			100, []string{"c1"},
		},
		{
			"rejected-access semantics: later ack after reactivation stays acked",
			func(tk *Tracker) {
				tk.Record("c1", "d", 90)
				tk.Ack("c1", "d", 92)
				tk.Record("c1", "d", 95)
				tk.Ack("c1", "d", 96)
			},
			100, nil,
		},
		{
			"multiple consumers sorted with inactive filtered",
			func(tk *Tracker) {
				tk.Record("old", "d", 50)
				tk.Record("moved", "d", 97)
				tk.Record("alpha", "d", 98)
				tk.Record("zeta", "d", 99)
				tk.Ack("moved", "d", 99)
			},
			100, []string{"alpha", "zeta"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk := New()
			tc.run(tk)
			got := tk.ActiveConsumers("d", tc.now, q)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("active = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasAccessed(t *testing.T) {
	tk := New()
	if tk.HasAccessed("c", "d") {
		t.Fatal("new consumer should not have accessed")
	}
	tk.Record("c", "d", 5)
	if !tk.HasAccessed("c", "d") {
		t.Fatal("consumer should have accessed after Record")
	}
}

func TestScannedBoundWithStaleConsumers(t *testing.T) {
	// 100 与 10000 两档早已不活跃的消费者：scanned 只取决于近期消费者数。
	for _, stale := range []int{100, 10000} {
		tk := New()
		for i := 0; i < stale; i++ {
			tk.Record("z"+itoa(i), "d", 1)
		}
		tk.Record("live1", "d", 95)
		tk.Record("live2", "d", 96)
		active := tk.ActiveConsumers("d", 100, 30)
		if len(active) != 2 {
			t.Fatalf("stale=%d active=%v", stale, active)
		}
		if got := tk.Scanned("d"); got > len(active)+1 {
			t.Fatalf("stale=%d scanned=%d, bound active+1=%d", stale, got, len(active)+1)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
