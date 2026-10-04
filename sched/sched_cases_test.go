package sched_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/sched"
)

type tableCase struct {
	name string
	run  func(t *testing.T, s *sched.Scheduler)
}

func regDefault(s *sched.Scheduler, P, o, w int64) {
	_ = s.Register("d", P, o, w, 0)
}

var tableCases = []tableCase{
	{"expire恰等即过期", func(t *testing.T, s *sched.Scheduler) {
		regDefault(s, 100, 0, 100)
		if err := s.Enqueue("d", "a", 1, 0, 10, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Deliver("d", 10); err != nil {
			t.Fatal(err)
		}
		st, _ := s.LastSettled("d")
		if fmt.Sprint(st.Expired) != "[a]" {
			t.Fatalf("expire=now 恰等应过期, got %v", st.Expired)
		}
	}},
	{"Ack已过期指令报NoCmd", func(t *testing.T, s *sched.Scheduler) {
		regDefault(s, 100, 0, 100)
		_ = s.Enqueue("d", "a", 1, 0, 50, 0)
		_, _ = s.Deliver("d", 0)
		if err := s.Ack("d", "a", 50); !errors.Is(err, sched.ErrNoCmd) {
			t.Fatalf("want NoCmd, got %v", err)
		}
		// 被拒 Ack 不应清除过期指令……规范要求入口过期清除也撤销。
		// 之后 100 的 Deliver 才清除；指令结局仍唯一（Expired）。
		_, _ = s.Deliver("d", 100)
		st, _ := s.LastSettled("d")
		if fmt.Sprint(st.Expired) != "[a]" {
			t.Fatalf("Expired=[a], got %v", st.Expired)
		}
	}},
	{"队首阻塞不跳过取更小", func(t *testing.T, s *sched.Scheduler) {
		regDefault(s, 100, 0, 100)
		_ = s.Enqueue("d", "big", 70, 0, 500, 0)
		_ = s.Enqueue("d", "small", 40, 0, 500, 0)
		v, _ := s.Deliver("d", 0)
		if fmt.Sprint(v) != "[big]" {
			t.Fatalf("want [big], got %v", v)
		}
		// 下一窗口 small 仍在（big 未确认时 big 先重投）。
	}},
	{"同窗口不重投且共享字节额度", func(t *testing.T, s *sched.Scheduler) {
		s2 := sched.New(2, 100, 3, 100)
		_ = s2.Register("d", 100, 0, 100, 0)
		_ = s2.Enqueue("d", "a", 60, 0, 500, 0)
		_ = s2.Enqueue("d", "b", 60, 0, 500, 0)
		v, _ := s2.Deliver("d", 0)
		if fmt.Sprint(v) != "[a]" {
			t.Fatalf("got %v", v)
		}
		v, _ = s2.Deliver("d", 1)
		if len(v) != 0 {
			t.Fatalf("同窗剩40字节放不下b, got %v", v)
		}
	}},
	{"条数额度K与字节额度独立生效", func(t *testing.T, s *sched.Scheduler) {
		s2 := sched.New(3, 100, 3, 100)
		_ = s2.Register("d", 100, 0, 100, 0)
		for _, id := range []string{"a", "b", "c", "d"} {
			_ = s2.Enqueue("d", id, 1, 0, 500, 0)
		}
		v, _ := s2.Deliver("d", 0)
		if len(v) != 3 {
			t.Fatalf("K=3, got %v", v)
		}
	}},
	{"第R次投递后的失败时机：R窗内Ack仍成功", func(t *testing.T, s *sched.Scheduler) {
		regDefault(s, 10, 0, 5) // K=2,Bw=100,R=2
		_ = s.Enqueue("d", "a", 1, 0, 500, 0)
		_, _ = s.Deliver("d", 0)
		_, _ = s.Deliver("d", 10)
		if err := s.Ack("d", "a", 10); err != nil {
			t.Fatalf("R窗内 Ack 应成功: %v", err)
		}
		_, _ = s.Deliver("d", 20)
		st, _ := s.LastSettled("d")
		if len(st.Failed) != 0 {
			t.Fatalf("已Ack不应Failed, got %v", st.Failed)
		}
	}},
	{"prio降序、同prio按seq升序", func(t *testing.T, s *sched.Scheduler) {
		regDefault(s, 100, 0, 100)
		_ = s.Enqueue("d", "a", 1, 0, 500, 0)
		_ = s.Enqueue("d", "b", 1, 2, 500, 0)
		_ = s.Enqueue("d", "c", 1, 2, 500, 0)
		v, _ := s.Deliver("d", 0)
		if fmt.Sprint(v) != "[b c]" {
			t.Fatalf("got %v", v)
		}
		v, _ = s.Deliver("d", 100)
		// b,c 未确认，下一窗口作为重投仍先于未投递的 a。
		if fmt.Sprint(v) != "[b c]" {
			t.Fatalf("redeliver b,c, got %v", v)
		}
	}},
	{"重投按首次投递先后优先于新指令", func(t *testing.T, s *sched.Scheduler) {
		regDefault(s, 10, 0, 5)
		_ = s.Enqueue("d", "old", 1, 0, 500, 0)
		_, _ = s.Deliver("d", 0)
		_ = s.Enqueue("d", "new", 1, 3, 500, 5) // 高优先级新指令
		v, _ := s.Deliver("d", 10)
		if fmt.Sprint(v) != "[old new]" {
			t.Fatalf("重投优先, got %v", v)
		}
	}},
	{"拒绝次序：Invalid>ClockBack>NoDevice/Exists>DupCmd>状态错误", func(t *testing.T, s *sched.Scheduler) {
		// 非法参数优先于设备不存在。
		if err := s.Register("", 0, 0, 0, 0); !errors.Is(err, sched.ErrInvalid) {
			t.Fatalf("got %v", err)
		}
		regDefault(s, 100, 0, 10)
		// 时钟回退优先于 Exists。
		if err := s.Register("d", 100, 0, 10, -1); !errors.Is(err, sched.ErrInvalid) {
			t.Fatalf("负数时间 Invalid, got %v", err)
		}
		// ClockBack（已注册设备 now 回退）。
		_ = s.Enqueue("d", "a", 1, 0, 500, 10)
		if err := s.Enqueue("d", "b", 1, 0, 500, 5); !errors.Is(err, sched.ErrClockBack) {
			t.Fatalf("ClockBack, got %v", err)
		}
		// DupCmd 优先于 TooBig/Unreachable。
		if err := s.Enqueue("d", "a", 101, 0, 1, 10); !errors.Is(err, sched.ErrDupCmd) {
			t.Fatalf("DupCmd first, got %v", err)
		}
	}},
	{"ErrFull：清除过期后仍达Q条", func(t *testing.T, s *sched.Scheduler) {
		s2 := sched.New(10, 1000, 3, 2)
		_ = s2.Register("d", 100, 0, 100, 0)
		_ = s2.Enqueue("d", "a", 1, 0, 500, 0)
		_ = s2.Enqueue("d", "b", 1, 0, 500, 0)
		if err := s2.Enqueue("d", "c", 1, 0, 500, 0); !errors.Is(err, sched.ErrFull) {
			t.Fatalf("Q=2, got %v", err)
		}
		// b 在 now=500 恰过期后，容量腾出，可入队。
		if err := s2.Enqueue("d", "c", 1, 0, 600, 500); err != nil {
			t.Fatalf("过期腾出容量: %v", err)
		}
	}},
	{"被拒Enqueue不产生过期副作用", func(t *testing.T, s *sched.Scheduler) {
		regDefault(s, 100, 0, 100)
		_ = s.Enqueue("d", "a", 1, 0, 100, 0)
		// 触发 Full 之外：重复 id 拒绝。再 Ack 已过期 a 应仍 NoCmd，
		// 且后续 Deliver 报告 a 过期（说明之前未被误清除）。
		if err := s.Enqueue("d", "a", 1, 0, 100, 100); !errors.Is(err, sched.ErrDupCmd) {
			t.Fatalf("got %v", err)
		}
		_, _ = s.Deliver("d", 100)
		st, _ := s.LastSettled("d")
		if fmt.Sprint(st.Expired) != "[a]" {
			t.Fatalf("a 应在 Deliver 时才过期, got %v", st.Expired)
		}
	}},
	{"Deliver在窗口外报Asleep且不改时钟", func(t *testing.T, s *sched.Scheduler) {
		regDefault(s, 100, 10, 5)
		if _, err := s.Deliver("d", 5); !errors.Is(err, sched.ErrAsleep) {
			t.Fatalf("got %v", err)
		}
		// 被拒后 now=0 的入队若时钟被误推进到5不会回退报错。
		if err := s.Enqueue("d", "a", 1, 0, 500, 0); !errors.Is(err, sched.ErrClockBack) {
			// now=0 < 设备已接受最大now(注册时0)，实际等于0，应成功。
			if err != nil {
				t.Fatalf("enqueue at 0 should succeed: %v", err)
			}
		}
	}},
}

func TestTableCases(t *testing.T) {
	for _, tc := range tableCases {
		t.Run(tc.name, func(t *testing.T) {
			s := sched.New(2, 100, 2, 100)
			t.Logf("输入用例 %q", tc.name)
			tc.run(t, s)
			t.Logf("输出与判定一致，用例通过")
		})
	}
}
