package lock

import "testing"

func TestCheckDelete(t *testing.T) {
	cases := []struct {
		name      string
		hold      bool
		mode      Mode
		until     uint64
		now       uint64
		bypass    bool
		canBypass bool
		want      Reason
	}{
		{"无保留", false, None, 0, 0, false, false, Allow},
		{"法律保留压过已到期保留", true, Compliance, 10, 100, false, false, ByLegalHold},
		{"法律保留压过一切", true, Governance, 100, 10, true, true, ByLegalHold},
		{"合规生效中不可绕过", false, Compliance, 100, 99, true, true, ByCompliance},
		{"合规恰到期", false, Compliance, 100, 100, false, false, Allow},
		{"治理生效中未绕过", false, Governance, 100, 99, false, true, ByGovernance},
		{"治理生效中绕过但无权限", false, Governance, 100, 99, true, false, ByGovernance},
		{"治理生效中合法绕过", false, Governance, 100, 99, true, true, Allow},
		{"治理恰到期", false, Governance, 100, 100, false, false, Allow},
		{"治理已过期", false, Governance, 100, 200, false, false, Allow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CheckDelete(c.hold, c.mode, c.until, c.now, c.bypass, c.canBypass); got != c.want {
				t.Fatalf("CheckDelete = %v, want %v", got, c.want)
			}
		})
	}
}

func TestCheckSetRetention(t *testing.T) {
	cases := []struct {
		name      string
		oldMode   Mode
		oldUntil  uint64
		newMode   Mode
		newUntil  uint64
		now       uint64
		bypass    bool
		canBypass bool
		want      Reason
	}{
		{"无保留任意设置", None, 0, Compliance, 50, 10, false, false, Allow},
		{"到期视同无保留", Compliance, 100, Governance, 50, 100, false, false, Allow},
		{"合规保持合规且更长", Compliance, 100, Compliance, 150, 10, false, false, Allow},
		{"合规保持合规等长", Compliance, 100, Compliance, 100, 10, false, false, Allow},
		{"合规缩短", Compliance, 100, Compliance, 99, 10, true, true, ByCompliance},
		{"合规降级治理", Compliance, 100, Governance, 200, 10, true, true, ByCompliance},
		{"合规清除", Compliance, 100, None, 0, 10, true, true, ByCompliance},
		{"治理等长", Governance, 100, Governance, 100, 20, false, false, Allow},
		{"治理升级合规且更长", Governance, 100, Compliance, 150, 20, false, false, Allow},
		{"治理升级合规但缩短", Governance, 100, Compliance, 99, 20, false, false, ByGovernance},
		{"治理缩短未绕过", Governance, 100, Governance, 50, 20, false, true, ByGovernance},
		{"治理缩短绕过无权限", Governance, 100, Governance, 50, 20, true, false, ByGovernance},
		{"治理缩短合法绕过", Governance, 100, Governance, 50, 20, true, true, Allow},
		{"治理清除需绕过", Governance, 100, None, 0, 20, false, true, ByGovernance},
		{"治理清除合法绕过", Governance, 100, None, 0, 20, true, true, Allow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CheckSetRetention(c.oldMode, c.oldUntil, c.newMode, c.newUntil, c.now, c.bypass, c.canBypass)
			if got != c.want {
				t.Fatalf("CheckSetRetention = %v, want %v", got, c.want)
			}
		})
	}
}
