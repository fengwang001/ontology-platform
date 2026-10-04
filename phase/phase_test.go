package phase

import "testing"

func TestMachineLifecycle(t *testing.T) {
	cases := []struct {
		name string
		run  func(m *Machine)
		chk  func(m *Machine) bool
	}{
		{"初始连续", func(m *Machine) {}, func(m *Machine) bool {
			return m.State() == Cont && m.HaltCount() == 0
		}},
		{"进入中断计数", func(m *Machine) { m.Enter(71) }, func(m *Machine) bool {
			return m.State() == Halt && m.HE() == 71 && m.HaltCount() == 1 && m.ExtCount() == 0
		}},
		{"延长更新 he 与次数", func(m *Machine) { m.Enter(71); m.Extend(131) }, func(m *Machine) bool {
			return m.State() == Halt && m.HE() == 131 && m.ExtCount() == 1 && m.HaltCount() == 1
		}},
		{"恢复清零本次延长", func(m *Machine) { m.Enter(71); m.Extend(131); m.Resume() }, func(m *Machine) bool {
			return m.State() == Cont && m.ExtCount() == 0 && m.HE() == 0 && m.HaltCount() == 1
		}},
		{"当日中断次数累计", func(m *Machine) { m.Enter(10); m.Resume(); m.Enter(20) }, func(m *Machine) bool {
			return m.State() == Halt && m.HaltCount() == 2 && m.ExtCount() == 0
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New()
			c.run(m)
			if !c.chk(m) {
				t.Fatal("状态断言失败")
			}
		})
	}
}
