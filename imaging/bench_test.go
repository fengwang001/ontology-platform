package imaging

import (
	"fmt"
	"testing"
)

// 基准步长：设备占用 40（20+清洁20），留观 [start+20,start+80)。
// 取 60 使同设备相邻留观恰相接（上一例 start+80 == 下一例 start+20），
// 设备占用间留 20 分钟空隙（不影响历史无关性的验证）。
const benchStep = 60

// 紧密相接的设备占用步长（不含留观约束）。
const denseStep = 40

// buildHistory 在每台设备铺 perDevice 个历史预约，共 devices*perDevice 个。
// step 控制同设备相邻开始间隔；capacity 为留观位总数。
func buildHistory(b *testing.B, devices, perDevice, step, capacity int) *Hospital {
	b.Helper()
	cfg := testConfig()
	cfg.ValidityNormal = maxTime // 基准内结果恒有效，剔除时效噪声
	cfg.ValidityHighRisk = maxTime
	cfg.ObservationCapacity = capacity
	cfg.CleaningMinutes[ClassCT] = 20 // 占用=20+20=40，与 benchStep 对齐
	h, err := NewHospital(cfg)
	if err != nil {
		b.Fatal(err)
	}
	mustOK(b, h.RegisterExamType(RegisterExamTypeRequest{ID: "e", Class: ClassCT, Duration: 20, Enhanced: true}), "exam")
	for d := 0; d < devices; d++ {
		did := fmt.Sprintf("dev%05d", d)
		mustOK(b, h.RegisterDevice(RegisterDeviceRequest{ID: did, Class: ClassCT}), "dev")
		for k := 0; k < perDevice; k++ {
			pid := fmt.Sprintf("p%05d-%04d", d, k)
			mustOK(b, h.RegisterPatient(RegisterPatientRequest{ID: pid, NoImplant: true}), "pat")
			mustOK(b, h.RecordKidney(RecordKidneyRequest{PatientID: pid, Value: 90, SampledAt: 0, Now: 0}), "kidney")
			mustOK(b, h.Book(BookRequest{
				ID: fmt.Sprintf("h%05d-%04d", d, k), PatientID: pid,
				ExamTypeID: "e", DeviceID: did, Start: k * step, Now: 0,
			}), "hist")
		}
	}
	return h
}

// BenchmarkBookVsHistory：全院历史总量两档（1e4 / 4e5），
// 新预约落在所有设备都空着的新窗口（start=5,000,000），
// “受影响时段内占用数”两档均为 0。若开销不随历史增长，ns/op 应同量级。
func BenchmarkBookVsHistory(b *testing.B) {
	cases := []struct {
		name            string
		devices, perDev int
	}{
		{"hist_10000", 100, 100},
		{"hist_400000", 2000, 200},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			h := buildHistory(b, c.devices, c.perDev, denseStep, c.devices*c.perDev+8)
			mustOK(b, h.RegisterPatient(RegisterPatientRequest{ID: "probe", NoImplant: true}), "probe")
			mustOK(b, h.RecordKidney(RecordKidneyRequest{PatientID: "probe", Value: 90, SampledAt: 0, Now: 0}), "probe k")
			b.ResetTimer()
			i := 0
			for b.Loop() {
				// 轮询不同设备，且每台目标设备新窗口为空；末尾 8 个槽足够放迭代探测。
				dev := i % c.devices
				start := 5_000_000 + (i/c.devices)*benchStep
				err := h.Book(BookRequest{
					ID: fmt.Sprintf("probe-%08d", i), PatientID: "probe",
					ExamTypeID: "e", DeviceID: fmt.Sprintf("dev%05d", dev), Start: start, Now: 0,
				})
				if err != nil {
					// 容量受限时留观冲突是预期；不记录为构造错误。
					b.Fatalf("unexpected err at i=%d: %v", i, err)
				}
				i++
			}
		})
	}
}

// BenchmarkObservationVsHistory：历史留观区间总数两档（2e4 / 4e5），
// 新预约的留观窗口与历史完全不相交（历史在早期、新约在 8,000,000），
// 受影响窗口事件数两档均为 0；容量核对开销应与历史总数无关。
func BenchmarkObservationVsHistory(b *testing.B) {
	cases := []struct {
		name            string
		devices, perDev int
	}{
		{"obs_20000", 200, 100},
		{"obs_400000", 4000, 100},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			h := buildHistory(b, c.devices, c.perDev, benchStep, 2*c.devices) // 每设备峰值 2
			mustOK(b, h.RegisterPatient(RegisterPatientRequest{ID: "probe", NoImplant: true}), "probe")
			mustOK(b, h.RecordKidney(RecordKidneyRequest{PatientID: "probe", Value: 90, SampledAt: 0, Now: 0}), "probe k")
			b.ResetTimer()
			i := 0
			for b.Loop() {
				dev := i % c.devices
				// 8,000,000 处设备空闲；其留观 [8,000,020, 8,000,060) 远离所有历史留观。
				err := h.Book(BookRequest{
					ID: fmt.Sprintf("obsprobe-%08d", i), PatientID: "probe",
					ExamTypeID: "e", DeviceID: fmt.Sprintf("dev%05d", dev), Start: 8_000_000 + (i/c.devices)*benchStep, Now: 0,
				})
				if err != nil {
					b.Fatalf("unexpected obs err i=%d: %v", i, err)
				}
				i++
			}
		})
	}
}
