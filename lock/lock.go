// Package lock 提供保留期与法律保留的只读判定。
package lock

// Mode 是保留模式。
type Mode int

const (
	// None 表示无保留。
	None Mode = iota
	// Governance 表示治理模式保留（可经权限绕过）。
	Governance
	// Compliance 表示合规模式保留（生效中不可绕过、不可缩短）。
	Compliance
)

// Valid 报告模式是否为合法枚举值。
func (m Mode) Valid() bool {
	return m == None || m == Governance || m == Compliance
}

// Reason 是保留类判定的结论。
type Reason int

const (
	// Allow 表示放行。
	Allow Reason = iota
	// ByLegalHold 表示被法律保留阻止。
	ByLegalHold
	// ByCompliance 表示被生效中的 COMPLIANCE 保留阻止。
	ByCompliance
	// ByGovernance 表示被生效中的 GOVERNANCE 保留阻止（未合法绕过）。
	ByGovernance
)

// CheckDelete 判定是否允许永久删除一个数据版本。
// 保留生效指 now 严格小于 until；恰等于视为已到期。
func CheckDelete(hold bool, mode Mode, until uint64, now uint64, bypass bool, canBypass bool) Reason {
	if hold {
		return ByLegalHold
	}
	if mode != None && now < until {
		if mode == Compliance {
			return ByCompliance
		}
		if !bypass || !canBypass {
			return ByGovernance
		}
	}
	return Allow
}

// CheckSetRetention 判定把 (oldMode, oldUntil) 改为 (newMode, newUntil) 是否允许。
// 无保留或已到期的保留视同无保留，任何设置都允许。
func CheckSetRetention(oldMode Mode, oldUntil uint64, newMode Mode, newUntil uint64, now uint64, bypass bool, canBypass bool) Reason {
	if oldMode == None || now >= oldUntil {
		return Allow
	}
	if oldMode == Compliance {
		if newMode == Compliance && newUntil >= oldUntil {
			return Allow
		}
		return ByCompliance
	}
	// 生效中的 GOVERNANCE：同级/升级且 until 不缩小时直接允许。
	if (newMode == Governance || newMode == Compliance) && newUntil >= oldUntil {
		return Allow
	}
	// 其余（缩短、清除等使 until 变小的变更）需合法绕过。
	if bypass && canBypass {
		return Allow
	}
	return ByGovernance
}
