package segment

import (
	"reflect"
	"testing"
)

// op 是封片器上的一步操作：push 累积画面组，seal 封残片（接管/断开），
// epoch 开新纪元。want 为期望封出的片，nil 表示不封片。
type op struct {
	kind string
	dur  int64
	want *Segment
}

func seg(seq, start, dur int64, disc bool, discBefore int64) *Segment {
	return &Segment{Seq: seq, Start: start, Dur: dur, Disc: disc, DiscBefore: discBefore}
}

func TestSegmenter(t *testing.T) {
	tests := []struct {
		name string
		d    int64
		ops  []op
	}{
		{
			name: "累积恰等D封片",
			d:    4000,
			ops: []op{
				{kind: "push", dur: 2000, want: nil},
				{kind: "push", dur: 2000, want: seg(0, 0, 4000, false, 0)},
			},
		},
		{
			name: "单个画面组超过D独自成片",
			d:    4000,
			ops: []op{
				{kind: "push", dur: 5000, want: seg(0, 0, 5000, false, 0)},
				{kind: "push", dur: 4000, want: seg(1, 5000, 4000, false, 0)},
			},
		},
		{
			name: "累积超过D整片封出不拆分",
			d:    4000,
			ops: []op{
				{kind: "push", dur: 3000, want: nil},
				{kind: "push", dur: 2000, want: seg(0, 0, 5000, false, 0)},
			},
		},
		{
			name: "残片不足不封",
			d:    4000,
			ops: []op{
				{kind: "push", dur: 3999, want: nil},
				{kind: "seal", want: seg(0, 0, 3999, false, 0)},
				{kind: "seal", want: nil}, // 残片为空不再产片
			},
		},
		{
			name: "D为1每个画面组都封片",
			d:    1,
			ops: []op{
				{kind: "push", dur: 1, want: seg(0, 0, 1, false, 0)},
				{kind: "push", dur: 60000, want: seg(1, 1, 60000, false, 0)},
			},
		},
		{
			name: "新纪元首片带标记但第0片不带",
			d:    1000,
			ops: []op{
				{kind: "push", dur: 1000, want: seg(0, 0, 1000, false, 0)}, // 纪元首片但 seq==0
				{kind: "epoch"},
				{kind: "push", dur: 1000, want: seg(1, 1000, 1000, true, 0)},
				{kind: "push", dur: 1000, want: seg(2, 2000, 1000, false, 1)},
				{kind: "epoch"},
				{kind: "push", dur: 1000, want: seg(3, 3000, 1000, true, 1)},
			},
		},
		{
			name: "接管时旧残片属旧纪元不因此带标记",
			d:    4000,
			ops: []op{
				{kind: "push", dur: 4000, want: seg(0, 0, 4000, false, 0)},
				{kind: "push", dur: 3000, want: nil},
				{kind: "seal", want: seg(1, 4000, 3000, false, 0)}, // 旧纪元残片短片
				{kind: "epoch"},
				{kind: "push", dur: 5000, want: seg(2, 7000, 5000, true, 0)},
			},
		},
		{
			name: "旧残片恰是旧纪元首片且非第0片则带标记",
			d:    4000,
			ops: []op{
				{kind: "push", dur: 4000, want: seg(0, 0, 4000, false, 0)},
				{kind: "epoch"},
				{kind: "push", dur: 1500, want: nil},
				{kind: "seal", want: seg(1, 4000, 1500, true, 0)}, // 纪元2首片
				{kind: "epoch"},
				{kind: "push", dur: 4000, want: seg(2, 5500, 4000, true, 1)},
			},
		},
		{
			name: "空残片接管不产片",
			d:    4000,
			ops: []op{
				{kind: "seal", want: nil},
				{kind: "epoch"},
				{kind: "push", dur: 4000, want: seg(0, 0, 4000, false, 0)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSegmenter(tt.d)
			for i, o := range tt.ops {
				var got *Segment
				switch o.kind {
				case "push":
					got = s.Push(o.dur)
				case "seal":
					got = s.SealPartial()
				case "epoch":
					s.BeginEpoch()
					continue
				}
				if !reflect.DeepEqual(got, o.want) {
					t.Fatalf("第 %d 步(%s)：得到 %+v，期望 %+v", i, o.kind, got, o.want)
				}
				t.Logf("第 %d 步 %s(dur=%d) -> %+v（判定依据：累积达 D 整封、GOP 不拆分、标记=纪元首片且 seq≠0）",
					i, o.kind, o.dur, got)
			}
		})
	}
}

// TestTimelineInvariant 验证相邻片媒体时间首尾相接、seq 连续无洞。
func TestTimelineInvariant(t *testing.T) {
	s := NewSegmenter(4000)
	durs := []int64{2000, 2000, 3000, 5000, 1, 3999, 60000}
	var prev *Segment
	for _, dur := range durs {
		got := s.Push(dur)
		if got == nil {
			continue
		}
		if prev != nil {
			if got.Seq != prev.Seq+1 || got.Start != prev.End() {
				t.Fatalf("时间线断裂：prev=%+v got=%+v", prev, got)
			}
		} else if got.Seq != 0 || got.Start != 0 {
			t.Fatalf("首片应为 seq0 起点0：%+v", got)
		}
		prev = got
	}
	if got := s.SealPartial(); got != nil {
		if prev != nil && (got.Seq != prev.Seq+1 || got.Start != prev.End()) {
			t.Fatalf("残片短片时间线断裂：prev=%+v got=%+v", prev, got)
		}
	}
}
