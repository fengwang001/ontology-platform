package interval

import (
	"testing"

	"ontology/bitemporal"
)

func i64(v int64) *int64 { return &v }

func TestValidate(t *testing.T) {
	v := NewValidator()
	cases := []struct {
		name     string
		rec      bitemporal.Record
		wantAxes []bitemporal.Axis
	}{
		{
			name: "两轴均自洽",
			rec: bitemporal.Record{
				Valid:       &bitemporal.Interval{Start: i64(1), End: i64(5)},
				Transaction: &bitemporal.Interval{Start: i64(2), End: i64(9)},
			},
		},
		{
			name: "有效轴起点晚于终点",
			rec: bitemporal.Record{
				Valid:       &bitemporal.Interval{Start: i64(10), End: i64(5)},
				Transaction: &bitemporal.Interval{Start: i64(2), End: i64(9)},
			},
			wantAxes: []bitemporal.Axis{bitemporal.ValidTime},
		},
		{
			name: "事务轴起点晚于终点",
			rec: bitemporal.Record{
				Transaction: &bitemporal.Interval{Start: i64(9), End: i64(1)},
			},
			wantAxes: []bitemporal.Axis{bitemporal.TransactionTime},
		},
		{
			name: "两轴均不自洽且顺序固定为有效在前",
			rec: bitemporal.Record{
				Valid:       &bitemporal.Interval{Start: i64(6), End: i64(5)},
				Transaction: &bitemporal.Interval{Start: i64(6), End: i64(5)},
			},
			wantAxes: []bitemporal.Axis{bitemporal.ValidTime, bitemporal.TransactionTime},
		},
		{
			name: "相同端点的开放区间为空集但自洽",
			rec: bitemporal.Record{
				Valid: &bitemporal.Interval{Start: i64(3), End: i64(3),
					StartClosed: bitemporal.Open, EndClosed: bitemporal.Open},
			},
		},
		{
			name: "无界区间自洽",
			rec:  bitemporal.Record{Valid: &bitemporal.Interval{End: i64(3)}},
		},
		{
			name: "缺失的轴不参与校验",
			rec:  bitemporal.Record{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := v.Validate(tc.rec)
			if len(got) != len(tc.wantAxes) {
				t.Fatalf("错误数量 = %d, 期望 %d (%v)", len(got), len(tc.wantAxes), got)
			}
			for i, axis := range tc.wantAxes {
				if got[i].Axis != axis {
					t.Fatalf("第 %d 个错误轴 = %s, 期望 %s", i, got[i].Axis, axis)
				}
			}
		})
	}
}

func TestValidateDoesNotMutate(t *testing.T) {
	v := NewValidator()
	rec := bitemporal.Record{
		Valid:       &bitemporal.Interval{Start: i64(10), End: i64(5)},
		Transaction: &bitemporal.Interval{Start: i64(1), End: i64(5)},
	}
	snapshot := *rec.Valid
	v.Validate(rec)
	if *rec.Valid != snapshot {
		t.Fatal("校验过程修改了入参记录")
	}
}
