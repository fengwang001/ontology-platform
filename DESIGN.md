# 构建矩阵展开与并行扇出执行器 - 设计说明

## 包划分
- `matrix`：纯函数式展开。`New(Config)` 先校验参数，再做笛卡尔积、exclude、include，
  产出确定次序的作业列表；不感知执行。
- `fanout`：单轮执行状态机。`Start/Finish/CancelAll`，维护状态数组、`next` 指针与
  running/pending 计数；不感知轮次。
- `attempt`：多轮账本。组合 fanout，加轮次上限 A、Rerun 与每作业 Running 次数；
  对外是唯一入口（`attempt.New` 校验 P/A 后委托 `matrix.New`）。

## 关键取舍与被放弃的方案
- include 只匹配"留存的基础组合"，不匹配先前追加的组合（规格明确）。放弃"追加组合
  可被后续 include 扩充"的方案：它会使结果依赖追加次序的解释，难以逐字节复现。
- 快停时立即把 Running/Pending 转 Cancelled，无确认阶段（规格明确）。放弃"等在跑
  作业自然结束再取消"：后者需要引入 Cancelling 中间态，迟到的 Finish 语义模糊。
- 追加组合的键值次序定为"轴键按轴声明序、附加键按字典序"，附加键写入基础组合时
  按字典序追加、覆盖不改位置：保证相同配置展开逐字节相同（map 迭代序不可依赖）。
- Finish 找下一个待启动作业用升序下标队列：Pending 作业的下标按序入队，Finish 只
  检视队首 1 个作业即启动，与 n 无关（scanned 计数器佐证）。放弃"next 单调指针"
  方案：Rerun 后 Pending 下标被 Succeeded 隔断、不再连续，不变式不成立；也放弃
  "每次线性扫描"的朴素实现。
- Rerun 重置时一次性扫描重建队列（扫描发生在 Rerun 而非 Finish，不受约束）。
- CancelAll 仅在已 Start 且未终结时生效，其余为无操作：避免 Start 前取消导致的
  不变式复杂化。
- 错误用哨兵加 `errors.Is`：参数非法/状态不符/重跑超限/矩阵过大/空矩阵各自可区分；
  校验全部先于任何状态修改，被拒绝的操作不改状态。
- 并发：每个 Executor 一把互斥锁串行化，attempt 到 fanout 固定锁序，等价串行执行。

## 拒绝次序
- New：参数非法（轴/exclude/include/P/A）大于 矩阵过大 大于 空矩阵。
- 运行期：参数非法（下标越界）大于 状态不符 大于 重跑超限。

## 本地验证
- `go test ./...`：表驱动单测加 1500 组随机配置与操作序列对照朴素模拟，日志含输入、
  输出与判定依据，固定种子可复现。
- `go test -race ./...` 验证并发等价串行；`go vet ./...`、`gofmt -l .`。
