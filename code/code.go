// Package code 描述工作流代码：一个有序的项序列，项只有两种：
//
//   - Step(name)：一个工作流步骤；
//   - Branch(pid, N, O)：补丁分支，pid 为补丁标记，N 为打补丁后的新序列，
//     O 为打补丁前的旧序列；N、O 只允许包含 Step，不得嵌套 Branch。
//
// 序列以值类型表达，Validate 负责全部结构与数量校验；非法 Code 不会被
// replay.Run 执行。
package code

// MaxItems 是顶层项序列的长度范围上限（至少 1 项）。
const MaxItems = 1000

// MaxBranchItems 是单个 Branch 的 N、O 各自允许包含的 Step 数上限。
const MaxBranchItems = 1000

// Kind 区分代码项种类。
type Kind uint8

const (
	KindStep Kind = iota + 1
	KindBranch
)

// Item 是代码序列中的一项。
//
// Kind 为 KindStep 时仅 Name 有效；Kind 为 KindBranch 时 Pid、New、Old
// 有效，New/Old 中的项必须全部是 KindStep（由 Validate 强制）。
type Item struct {
	Kind Kind
	Name []byte
	Pid  []byte
	New  []Item
	Old  []Item
}

// Code 是工作流代码：有序的顶层项序列（1..MaxItems 项）。
type Code []Item

// Step 构造一个步骤项 Step(name)。
func Step(name []byte) Item {
	return Item{Kind: KindStep, Name: name}
}

// Branch 构造一个补丁分支项 Branch(pid, N, O)。N/O 可为空，
// 但只能含 Step；嵌套是否合法由 Validate 统一报告。
func Branch(pid []byte, n, o []Item) Item {
	return Item{Kind: KindBranch, Pid: pid, New: n, Old: o}
}

// IsStep 报告该项是否为 Step。
func (it Item) IsStep() bool { return it.Kind == KindStep }

// IsBranch 报告该项是否为 Branch。
func (it Item) IsBranch() bool { return it.Kind == KindBranch }
