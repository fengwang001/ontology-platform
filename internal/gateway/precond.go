package gateway

// hasPrecondition 报告指令类型是否有前置条件。关空调与寻车无前置条件。
func hasPrecondition(t CmdType) bool {
	switch t {
	case CmdACOff, CmdFindCar:
		return false
	}
	return true
}

// precondOK 判定指令前置条件是否满足。
// known 为 false（状态陈旧或从未上报）时，对有前置条件的指令一律视为不满足。
func precondOK(t CmdType, s StateReport, known bool, minBatteryPct int) bool {
	if !hasPrecondition(t) {
		return true
	}
	if !known {
		return false
	}
	switch t {
	case CmdUnlock, CmdOpenTrunk:
		// 解锁与开后备箱：车速为 0 且驻车。
		return s.SpeedKmh == 0 && s.Gear == GearPark
	case CmdLock:
		// 上锁：车速为 0。
		return s.SpeedKmh == 0
	case CmdACOn:
		// 开空调：电量不低于下限且电源状态不是行驶。
		return s.BatteryPct >= minBatteryPct && s.Power != PowerDriving
	case CmdRemoteStart:
		// 远程启动：驻车、车门全锁、电量不低于下限。
		return s.Gear == GearPark && s.Lock == LockAllLocked && s.BatteryPct >= minBatteryPct
	}
	return true
}

// conflicts 报告两条在途指令是否互斥。矩阵：
//   - 同类型互斥（重复提交）
//   - 解锁 ⊗ 上锁
//   - 开空调 ⊗ 关空调
//   - 解锁 ⊗ 远程启动
//   - 上锁与远程启动不互斥，其余组合均不互斥
func conflicts(a, b CmdType) bool {
	if a == b {
		return true
	}
	pair := func(x, y CmdType) bool {
		return (a == x && b == y) || (a == y && b == x)
	}
	return pair(CmdUnlock, CmdLock) ||
		pair(CmdACOn, CmdACOff) ||
		pair(CmdUnlock, CmdRemoteStart)
}
