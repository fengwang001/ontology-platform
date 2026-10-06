package alarm_test

import (
	"fmt"
	"testing"

	"ontology/alarm"
)

// benchCfg 关闭震荡与自动屏蔽，避免基准中触发被隐藏，保证每次 trigger 可测。
func benchCfg() alarm.Config {
	return alarm.Config{
		ChatWindowSec:    0,
		ChatCount:        1_000_000,
		ChatShelveSec:    0,
		HighShelveMaxSec: 1 << 30,
		LowShelveMaxSec:  1 << 30,
	}
}

// BenchmarkTriggerVsHistory 对照两档“历史事件总量”：
// 在同一个点上先制造 H 个被接受的历史事件（触发/返回/确认），
// 再测量随后每次操作的耗时。若每次开销随历史增长，第二档应明显变慢。
func BenchmarkTriggerVsHistory(b *testing.B) {
	for _, hist := range []int{10_000, 40_000} {
		b.Run(fmt.Sprintf("history=%d", hist), func(b *testing.B) {
			s := alarm.New(benchCfg(), []alarm.PointConfig{{ID: 1, Priority: alarm.PriorityHigh}}, nil)
			ts := 0
			for i := 0; i < hist; i++ {
				if err := s.Trigger(ts, 1); err != nil {
					b.Fatal(err)
				}
				ts++
				if err := s.Return(ts, 1); err != nil {
					b.Fatal(err)
				}
				if err := s.Ack(ts, 1, alarm.RoleOperator, "op"); err != nil {
					b.Fatal(err)
				}
				ts++
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.Trigger(ts, 1); err != nil {
					b.Fatal(err)
				}
				ts++
				if err := s.Return(ts, 1); err != nil {
					b.Fatal(err)
				}
				if err := s.Ack(ts, 1, alarm.RoleOperator, "op"); err != nil {
					b.Fatal(err)
				}
				ts++
			}
			b.ReportMetric(float64(hist), "hist-events")
		})
	}
}

// makePoints 构造 total 个点；前 activeN 个触发为活动状态，其余保持正常。
func makePoints(total, activeN int) ([]alarm.PointConfig, *alarm.Service, int) {
	confs := make([]alarm.PointConfig, total)
	for i := range confs {
		confs[i] = alarm.PointConfig{ID: i + 1, Priority: alarm.PriorityHigh}
	}
	s := alarm.New(benchCfg(), confs, nil)
	ts := 1
	for i := 0; i < activeN; i++ {
		if err := s.Trigger(ts, i+1); err != nil {
			panic(err)
		}
	}
	return confs, s, ts
}

// BenchmarkActiveListVsActive 对照：
//   - 总点数 10_000、活动 500
//   - 总点数 40_000、活动 500（总点数不同、活动条数相同 -> 耗时应几乎相同，
//     证明查询不随“总点数/历史”增长）
//   - 总点数 40_000、活动 4_000（活动条数变多 -> 耗时随之增长，
//     证明查询开销只随活动条数变化）
func BenchmarkActiveListVsActive(b *testing.B) {
	cases := []struct {
		total, active int
	}{
		{10_000, 500},
		{40_000, 500},
		{40_000, 4_000},
	}
	for _, c := range cases {
		b.Run(fmt.Sprintf("total=%d,active=%d", c.total, c.active), func(b *testing.B) {
			_, s, ts := makePoints(c.total, c.active)
			list, err := s.ActiveList(ts)
			if err != nil {
				b.Fatal(err)
			}
			if len(list) != c.active {
				b.Fatalf("setup: want %d active, got %d", c.active, len(list))
			}
			b.ResetTimer()
			var sink int
			for i := 0; i < b.N; i++ {
				out, err := s.ActiveList(ts)
				if err != nil {
					b.Fatal(err)
				}
				sink = len(out)
			}
			b.StopTimer()
			b.ReportMetric(float64(c.active), "active-alarms")
			_ = sink
		})
	}
}

// BenchmarkAlarmRate 验证报警率查询为二分查找（O(log 出现次数)），不随历史线性增长。
func BenchmarkAlarmRate(b *testing.B) {
	for _, appears := range []int{10_000, 40_000} {
		b.Run(fmt.Sprintf("appearances=%d", appears), func(b *testing.B) {
			s := alarm.New(benchCfg(), []alarm.PointConfig{{ID: 1, Priority: alarm.PriorityHigh}}, nil)
			ts := 0
			for i := 0; i < appears; i++ {
				if err := s.Trigger(ts, 1); err != nil {
					b.Fatal(err)
				}
				ts++
				if err := s.Return(ts, 1); err != nil {
					b.Fatal(err)
				}
				if err := s.Ack(ts, 1, alarm.RoleOperator, "op"); err != nil {
					b.Fatal(err)
				}
				ts++
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.AlarmRate(ts, 1000); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
