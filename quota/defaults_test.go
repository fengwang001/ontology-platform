package quota

import (
	"reflect"
	"testing"
)

func i64(v int64) *int64 { return &v }

// TestDefaultPodBranches 覆盖补全的各分支。
func TestDefaultPodBranches(t *testing.T) {
	def := Defaults{Rules: map[string]DefaultRule{
		"cpu": {Request: i64(1), Limit: i64(2)},
		"mem": {Limit: i64(8)},
		"gpu": {Request: i64(3)},
	}}
	cases := []struct {
		name      string
		spec      PodSpec
		wantReq   map[string]int64
		wantLimit map[string]int64
	}{
		{
			name:      "已声明的请求与上限不被补全改变",
			spec:      PodSpec{Requests: map[string]int64{"cpu": 5}, Limits: map[string]int64{"cpu": 7}},
			wantReq:   map[string]int64{"cpu": 5, "mem": 8, "gpu": 3},
			wantLimit: map[string]int64{"cpu": 7, "mem": 8},
		},
		{
			name:      "缺省上限取默认上限",
			spec:      PodSpec{Requests: map[string]int64{"cpu": 5}},
			wantReq:   map[string]int64{"cpu": 5, "mem": 8, "gpu": 3},
			wantLimit: map[string]int64{"cpu": 2, "mem": 8},
		},
		{
			name:      "缺省请求取已声明的上限",
			spec:      PodSpec{Limits: map[string]int64{"cpu": 9}},
			wantReq:   map[string]int64{"cpu": 9, "mem": 8, "gpu": 3},
			wantLimit: map[string]int64{"cpu": 9, "mem": 8},
		},
		{
			name:      "缺省请求取补全后的默认上限",
			spec:      PodSpec{Requests: map[string]int64{}},
			wantReq:   map[string]int64{"cpu": 2, "mem": 8, "gpu": 3},
			wantLimit: map[string]int64{"cpu": 2, "mem": 8},
		},
		{
			name:      "无上限时缺省请求取默认请求",
			spec:      PodSpec{},
			wantReq:   map[string]int64{"gpu": 3, "cpu": 2, "mem": 8},
			wantLimit: map[string]int64{"cpu": 2, "mem": 8},
		},
		{
			name:      "无声明且无默认值的资源保持缺省",
			spec:      PodSpec{Requests: map[string]int64{"disk": 4}},
			wantReq:   map[string]int64{"disk": 4, "cpu": 2, "mem": 8, "gpu": 3},
			wantLimit: map[string]int64{"cpu": 2, "mem": 8},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eff := defaultPod(tc.spec, def)
			t.Logf("输入 spec=%+v 默认值=%v", tc.spec, def.Rules)
			t.Logf("输出 requests=%v limits=%v", eff.Requests, eff.Limits)
			if !reflect.DeepEqual(eff.Requests, tc.wantReq) {
				t.Errorf("requests = %v, 期望 %v", eff.Requests, tc.wantReq)
			}
			if !reflect.DeepEqual(eff.Limits, tc.wantLimit) {
				t.Errorf("limits = %v, 期望 %v", eff.Limits, tc.wantLimit)
			}
		})
	}
}

// TestDefaultPodNoDefaults 无默认值规则时，未声明的资源保持缺省（不是 0）。
func TestDefaultPodNoDefaults(t *testing.T) {
	eff := defaultPod(PodSpec{}, Defaults{})
	t.Logf("输入 空 spec 空默认值; 输出 requests=%v limits=%v terminating=%v",
		eff.Requests, eff.Limits, eff.Terminating)
	if len(eff.Requests) != 0 || len(eff.Limits) != 0 {
		t.Fatalf("期望全部缺省, 得到 %+v", eff)
	}
	if eff.Terminating {
		t.Fatal("未声明截止时长应为无期限")
	}
}

// TestDefaultPodTerminating 期限判定：>0 有期限，nil/0 无期限。
func TestDefaultPodTerminating(t *testing.T) {
	for _, tc := range []struct {
		deadline *int64
		want     bool
	}{
		{nil, false},
		{i64(0), false},
		{i64(30), true},
	} {
		eff := defaultPod(PodSpec{DeadlineSeconds: tc.deadline}, Defaults{})
		t.Logf("输入 deadline=%v; 输出 terminating=%v; 依据 截止时长>0 才有期限", tc.deadline, eff.Terminating)
		if eff.Terminating != tc.want {
			t.Errorf("deadline=%v terminating=%v, 期望 %v", tc.deadline, eff.Terminating, tc.want)
		}
	}
}

// TestClassify 服务等级判定：全部缺省或为零才是尽力型。
func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		eff  EffectivePod
		want bool
	}{
		{"全缺省", EffectivePod{Requests: map[string]int64{}, Limits: map[string]int64{}}, true},
		{"显式零", EffectivePod{Requests: map[string]int64{"cpu": 0}, Limits: map[string]int64{"cpu": 0}}, true},
		{"请求非零", EffectivePod{Requests: map[string]int64{"cpu": 1}}, false},
		{"上限非零", EffectivePod{Limits: map[string]int64{"mem": 1}}, false},
	}
	for _, tc := range cases {
		got := classify(tc.eff)
		t.Logf("输入 %+v; 输出 bestEffort=%v; 依据 请求与上限均缺省或为零才是尽力型", tc.eff, got.BestEffort)
		if got.BestEffort != tc.want {
			t.Errorf("%s: bestEffort=%v, 期望 %v", tc.name, got.BestEffort, tc.want)
		}
	}
}
