package capture

import "testing"

// TestRequiredScenarios 覆盖题目点名的全部行为场景，
// 每一步都与朴素模拟对照，并在 -v 日志中打印输入、输出与判定依据。
func TestRequiredScenarios(t *testing.T) {
	t.Run("同槽两次捕获共享同一句柄", func(t *testing.T) {
		runScenario(t, "same-slot-shared", []op{
			{kind: "push", a: 10},
			{kind: "capture", a: 0}, // 期望句柄 1
			{kind: "capture", a: 0}, // 复用句柄 1，持有数变为 2
			{kind: "release", a: 1}, // 持有数回到 1，仍开放
			{kind: "capture", a: 0}, // 仍复用句柄 1
		})
	})

	t.Run("写经开放变量直接改栈槽且双向可见", func(t *testing.T) {
		runScenario(t, "open-write-through", []op{
			{kind: "push", a: 1},
			{kind: "push", a: 2},
			{kind: "capture", a: 1},           // h=1 绑定槽 1
			{kind: "handle_set", a: 1, b: 22}, // 经句柄写 -> 栈槽 1
			{kind: "slot_get", a: 1},          // 直接读槽看到 22
			{kind: "slot_set", a: 1, b: 33},   // 直接写槽
			{kind: "handle_get", a: 1},        // 句柄读到 33
		})
	})

	t.Run("关闭后句柄与栈互不影响", func(t *testing.T) {
		runScenario(t, "closed-isolated", []op{
			{kind: "push", a: 5},
			{kind: "capture", a: 0}, // h=1
			{kind: "handle_set", a: 1, b: 7},
			{kind: "close", a: 0},             // 复制槽值 7 后关闭，栈空
			{kind: "handle_get", a: 1},        // 关闭变量读到自身存储 7
			{kind: "handle_set", a: 1, b: 70}, // 只改自身存储
			{kind: "handle_get", a: 1},        // 仍是 70
			{kind: "push", a: 99},             // 槽 0 重新压栈
			{kind: "slot_get", a: 0},          // 槽值 99，关闭变量不受影响
			{kind: "handle_get", a: 1},        // 关闭变量仍是 70
			{kind: "slot_set", a: 0, b: 100},  // 改栈不影响关闭变量
			{kind: "handle_get", a: 1},
		})
	})

	t.Run("关闭层恰等于栈顶是合法空操作", func(t *testing.T) {
		runScenario(t, "close-at-top-noop", []op{
			{kind: "push", a: 3},
			{kind: "push", a: 4},
			{kind: "capture", a: 0}, // h=1
			{kind: "capture", a: 1}, // h=2
			{kind: "close", a: 2},   // level==栈顶：无变量关闭，栈不变
			{kind: "handle_get", a: 1},
			{kind: "handle_get", a: 2},
			{kind: "close", a: 2}, // 再来一次仍为空操作
			{kind: "slot_set", a: 1, b: 44},
			{kind: "handle_get", a: 2}, // 仍开放，看到 44
		})
	})

	t.Run("关闭后同槽重新压栈再捕获得新句柄", func(t *testing.T) {
		runScenario(t, "recapture-after-close", []op{
			{kind: "push", a: 8},
			{kind: "capture", a: 0}, // h=1
			{kind: "close", a: 0},   // h=1 关闭
			{kind: "push", a: 80},   // 槽 0 重新占用
			{kind: "capture", a: 0}, // 新句柄 h=2，开放于新栈
			{kind: "handle_set", a: 2, b: 81},
			{kind: "slot_get", a: 0},   // 新句柄写穿透到槽
			{kind: "handle_get", a: 1}, // 老句柄保持关闭值 8
			{kind: "close", a: 0},
			{kind: "handle_get", a: 2}, // 新句柄关闭值 81
		})
	})

	t.Run("持有数归零后摘除再捕获得新句柄", func(t *testing.T) {
		runScenario(t, "recapture-after-release", []op{
			{kind: "push", a: 6},
			{kind: "capture", a: 0},         // h=1
			{kind: "capture", a: 0},         // 复用 h=1，持有数 2
			{kind: "release", a: 1},         // 持有数 1
			{kind: "capture", a: 0},         // 仍复用 h=1，持有数 2
			{kind: "release", a: 1},         // 持有数 1
			{kind: "release", a: 1},         // 归零：从共享表摘除（变量仍开放于槽但无句柄）
			{kind: "handle_get", a: 1},      // 拒绝：已释放完
			{kind: "slot_set", a: 0, b: 66}, // 槽仍可直接写
			{kind: "capture", a: 0},         // 得到全新句柄 h=2
			{kind: "handle_get", a: 2},      // 开放读取看到槽值 66
			{kind: "release", a: 1},         // 拒绝：重复释放已归零句柄
		})
	})

	t.Run("两个层级的部分关闭", func(t *testing.T) {
		runScenario(t, "partial-close-two-levels", []op{
			{kind: "push", a: 1},
			{kind: "push", a: 2},
			{kind: "push", a: 3},
			{kind: "capture", a: 0}, // h=1 槽0
			{kind: "capture", a: 1}, // h=2 槽1
			{kind: "capture", a: 2}, // h=3 槽2
			{kind: "close", a: 2},   // 仅槽2的 h=3 关闭（复制值3）；h1/h2 保持开放
			{kind: "handle_get", a: 1},
			{kind: "handle_get", a: 2},
			{kind: "handle_get", a: 3}, // 关闭，读到 3
			{kind: "slot_set", a: 1, b: 22},
			{kind: "handle_get", a: 2},        // h2 开放，读到 22
			{kind: "handle_set", a: 3, b: 30}, // h3 关闭，只改自身
			{kind: "close", a: 1},             // 再关到层1：槽1 的 h2 关闭（复制22），h1 仍开放
			{kind: "handle_get", a: 1},        // h1 开放读槽0
			{kind: "handle_get", a: 2},        // h2 关闭读 22
			{kind: "handle_get", a: 3},        // h3 关闭读 30
			{kind: "slot_set", a: 0, b: 11},
			{kind: "handle_get", a: 1}, // h1 开放，读到 11
		})
	})

	t.Run("全部拒绝原因且被拒绝操作不改变状态", func(t *testing.T) {
		runScenario(t, "rejections-atomic", []op{
			{kind: "capture", a: 0},          // 空栈捕获槽0：不小于栈顶
			{kind: "capture", a: -1},         // 负槽号同样拒绝
			{kind: "close", a: -1},           // level<0
			{kind: "close", a: 1},            // level>栈顶0
			{kind: "slot_get", a: 0},         // 空栈读槽
			{kind: "slot_set", a: 0, b: 9},   // 空栈写槽
			{kind: "handle_get", a: 7},       // 句柄不存在
			{kind: "handle_set", a: 7, b: 1}, // 句柄不存在
			{kind: "release", a: 7},          // 句柄不存在
			{kind: "push", a: 42},
			{kind: "close", a: 2},            // 栈顶1，level=2 越界拒绝且栈不变
			{kind: "slot_get", a: 0},         // 拒绝未改状态：槽0 仍是 42
			{kind: "slot_get", a: 1},         // 栈顶仍为1，槽1越界
			{kind: "capture", a: 1},          // 槽1==栈顶，拒绝
			{kind: "capture", a: 0},          // h=1
			{kind: "release", a: 1},          // 归零摘除
			{kind: "handle_get", a: 1},       // 已释放完（与不存在互斥）
			{kind: "handle_set", a: 1, b: 2}, // 已释放完
			{kind: "release", a: 1},          // 重复释放：已释放完
			{kind: "handle_get", a: 999},     // 仍不存在，不是已释放完
		})
	})
}
