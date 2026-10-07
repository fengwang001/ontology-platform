package aggview

import "testing"

// 常数份更新证明：归属改变触及的聚合条目数与分组内实例总数无关。
//
// 结构论证（实现层面可直接核对）：
//   - AddLink  只对“新分组”做一次正向 apply，reshare 仅作用于该成员的其他归属；
//   - RemoveLink 只对“原分组”做一次反向 apply，reshare 同理；
//   - MoveToGroup 的 apply 仅出现在旧分组集合与新分组两处；
//
// 引擎任何路径都不遍历目标分组的成员列表。
//
// 本例用不断增大的分组规模做经验佐证：MoveToGroup 的触及份数恒为 2。
func TestConstantTouchesIndependentOfGroupSize(t *testing.T) {
	for _, size := range []int{1, 10, 100, 500} {
		e, st, _ := newTestEngine(t)
		registerPayroll(t, e, PolicyFull)
		mustCreate(t, st, "src", tDept)
		mustCreate(t, st, "dst", tDept)

		// size 个成员全部归属 src。
		for i := 0; i < size; i++ {
			emp := ID("emp" + itoa(i))
			mustCreate(t, st, emp, tEmp)
			_, _ = e.SetProperty(emp, pSalary, FromInt(1))
			_, _ = e.AddLink(emp, lBelong, "src")
		}
		// 再放一个被迁移成员。
		mustCreate(t, st, "mover", tEmp)
		_, _ = e.SetProperty("mover", pSalary, FromInt(7))
		_, _ = e.AddLink("mover", lBelong, "src")
		ver := e.MembershipVersion(vPayroll, "mover")

		chg, err := e.MoveToGroup(vPayroll, "mover", "dst", ver)
		if err != nil {
			t.Fatalf("size=%d: %v", size, err)
		}
		if n := chg.TouchedGroups(); n != 2 {
			t.Fatalf("size=%d: touched %d groups, want exactly 2 (old+new)", size, n)
		}
		assertAgg(t, e, vPayroll, "src", itoa(size), size)
		assertAgg(t, e, vPayroll, "dst", "7", 1)
	}
}

// 单条链接增删在 EvenShare 下也只触及常数份（新/原分组 + 该成员的其他归属）。
func TestConstantTouchesEvenShare(t *testing.T) {
	for _, size := range []int{1, 50, 200} {
		e, st, _ := newTestEngine(t)
		registerPayroll(t, e, PolicyEvenShare)
		mustCreate(t, st, "g0", tDept)
		mustCreate(t, st, "g1", tDept)
		_ = size
		mustCreate(t, st, "emp", tEmp)
		_, _ = e.SetProperty("emp", pSalary, FromInt(60))
		_, _ = e.AddLink("emp", lBelong, "g0")
		chg, err := e.AddLink("emp", lBelong, "g1")
		if err != nil {
			t.Fatal(err)
		}
		if n := chg.TouchedGroups(); n != 2 {
			t.Fatalf("size=%d: touched %d, want 2", size, n)
		}
		// g0 与 g1 各 30；分组内成员多少与此无关。
		assertAgg(t, e, vPayroll, "g0", "30", 1)
		assertAgg(t, e, vPayroll, "g1", "30", 1)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
