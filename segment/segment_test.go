package segment_test

import (
	"testing"

	"ontology/dvr"
	"ontology/segment"
)

type step struct {
	op      string // "push", "flush" or "epoch"
	dur     int64
	wantOK  bool
	wantSeq int64
	want    dvr.Segment
}

func TestSegmenter(t *testing.T) {
	tests := []struct {
		name  string
		d     int64
		steps []step
	}{
		{
			name: "累积恰等D封片",
			d:    4000,
			steps: []step{
				{op: "push", dur: 2000, wantOK: false},
				{op: "push", dur: 2000, wantOK: true, want: dvr.Segment{Seq: 0, Start: 0, Dur: 4000}},
			},
		},
		{
			name: "单个画面组超过D独自成片",
			d:    4000,
			steps: []step{
				{op: "push", dur: 5000, wantOK: true, want: dvr.Segment{Seq: 0, Start: 0, Dur: 5000}},
			},
		},
		{
			name: "残片与大组一起封片不拆分",
			d:    4000,
			steps: []step{
				{op: "push", dur: 3000, wantOK: false},
				{op: "push", dur: 5000, wantOK: true, want: dvr.Segment{Seq: 0, Start: 0, Dur: 8000}},
			},
		},
		{
			name: "残片为空flush不出片",
			d:    4000,
			steps: []step{
				{op: "epoch"},
				{op: "flush", wantOK: false},
			},
		},
		{
			name: "残片非空flush成短片且媒体时间线连续",
			d:    4000,
			steps: []step{
				{op: "epoch"},
				{op: "push", dur: 5000, wantOK: true, want: dvr.Segment{Seq: 0, Start: 0, Dur: 5000}},
				{op: "push", dur: 1000, wantOK: false},
				{op: "flush", wantOK: true, want: dvr.Segment{Seq: 1, Start: 5000, Dur: 1000}},
			},
		},
		{
			name: "第0片不带标记各纪元首片带标记",
			d:    4000,
			steps: []step{
				{op: "epoch"},
				{op: "push", dur: 5000, wantOK: true, want: dvr.Segment{Seq: 0, Start: 0, Dur: 5000, Disc: false}},
				{op: "epoch"},
				{op: "push", dur: 5000, wantOK: true, want: dvr.Segment{Seq: 1, Start: 5000, Dur: 5000, Disc: true}},
				{op: "push", dur: 5000, wantOK: true, want: dvr.Segment{Seq: 2, Start: 10000, Dur: 5000, Disc: false}},
			},
		},
		{
			name: "接管残片属旧纪元首片仍带标记",
			d:    4000,
			steps: []step{
				{op: "epoch"},
				{op: "push", dur: 5000, wantOK: true, want: dvr.Segment{Seq: 0, Start: 0, Dur: 5000}},
				{op: "epoch"},
				{op: "push", dur: 1000, wantOK: false},
				{op: "flush", wantOK: true, want: dvr.Segment{Seq: 1, Start: 5000, Dur: 1000, Disc: true}},
			},
		},
		{
			name: "接管残片非纪元首片不带标记",
			d:    4000,
			steps: []step{
				{op: "epoch"},
				{op: "push", dur: 5000, wantOK: true, want: dvr.Segment{Seq: 0, Start: 0, Dur: 5000}},
				{op: "push", dur: 3000, wantOK: false},
				{op: "flush", wantOK: true, want: dvr.Segment{Seq: 1, Start: 5000, Dur: 3000, Disc: false}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := segment.New(tc.d)
			var prevEnd int64
			for i, st := range tc.steps {
				var got dvr.Segment
				var ok bool
				switch st.op {
				case "push":
					got, ok = s.Push(st.dur)
				case "flush":
					got, ok = s.Flush()
				case "epoch":
					s.BeginEpoch()
					continue
				}
				if ok != st.wantOK {
					t.Fatalf("step %d: ok=%v, want %v", i, ok, st.wantOK)
				}
				if !ok {
					continue
				}
				if got != st.want {
					t.Fatalf("step %d: seg=%+v, want %+v", i, got, st.want)
				}
				if got.Start != prevEnd {
					t.Fatalf("step %d: start=%d breaks contiguity, want %d", i, got.Start, prevEnd)
				}
				prevEnd = got.End()
			}
		})
	}
}
