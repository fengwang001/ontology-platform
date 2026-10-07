# 批量更新崩溃原子性设计说明

## 目标

一次批量更新在生效过程的任意时刻被中断（进程崩溃/断电）后，重启恢复必须
把系统收敛到两种且仅两种终态之一：

- **整批未生效**：所有相关实例的版本号与属性与批次发起前完全一致；
- **整批已生效**：所有相关实例的最终状态与批次完整生效后完全一致。

恢复逻辑自身也可能被再次中断，因此必须幂等；恢复扫描量不得随历史批次总数
增长；未决批次写集内的实例在终态确定前必须拒绝一切读写。

## 核心机制

### 唯一判定时刻：COMMIT 记录的持久化

"已被外部认定为已生效"的唯一判定时刻是 **COMMIT 记录写入批次日志段并完成
落盘（Sync）** 的那一刻：

- 此前任意中断 → 恢复归类为未生效，按前像（before-image）幂等撤销；
- 此后任意中断 → 恢复归类为已生效，按后像（after-image）幂等重做。

判定依据只有一个——恢复时批次段中是否存在**完整**的 COMMIT 记录。记录带
CRC 校验，撕裂写入的 COMMIT 行在扫描时被截断，等价于不存在，因此判定对
重复执行是确定且可复现的（`TestTornCommitRecord`）。

### 存储分层

| 组件 | 文件 | 作用 |
| --- | --- | --- |
| CONTROL | `control.json` | 固定大小：下一批次号、未决批次号、未决批次写集 |
| 批次日志段 | `journal/<id>.jlog` | 每批次一个段：BEGIN（写集+全部变更）、MUT（前像/后像）、COMMIT、DONE |
| 实例快照 | `store/snapshot.json` | 检查点时刻的全量实例状态 |
| 实例增量 | `store/delta.log` | 追加式实例记录（CRC 行），检查点时清空 |
| 批次状态 | `status/<id>.json` | 四种终态的持久化记录 |

模拟磁盘 `SimDisk` 复刻页缓存语义：`WriteFile/AppendFile` 只写易失缓冲，
`Sync` 才持久，`Crash()` 丢弃未落盘数据。崩溃原子性完全由"日志记录先于
数据落盘（WAL）+ 提交记录原子落盘"保证，不依赖任何单文件多步写的原子性。

### 执行管线（故障注入点）

`BeginControl → BeginJournal → JournalMut[i] → Apply[i] → PreCommit →
Commit → Checkpoint → Close → Done`

- **BeginControl**：CONTROL 登记批次号与写集并落盘——批次的"出生证明"，
  也是重启后重建写集锁的依据；
- **JournalMut[i]**：先写前像/后像日志并落盘（WAL：日志先于数据）；
- **Apply[i]**：变更应用到实例存储（steal：提交前即可落盘，因此恢复必须
  能撤销已应用的部分）；
- **Commit**：唯一判定时刻；
- **Checkpoint / Close**：快照重写、DONE、CONTROL 复位、状态落盘、段删除。

恢复管线：`RScan → RFix[i] → RFinalize → RDone`，同样每个阶段之间都可注入
中断。

### 幂等恢复

每个实例记录携带 `LastBatch` 戳（最后修改它的批次号）：

- **重做**：仅当实例未带本批次戳时应用后像；
- **撤销**：仅当实例仍带本批次戳时恢复前像。

因此无论恢复被执行一次还是多次、无论恢复自身被中断多少次，最终都收敛到同
一终态；版本号不会因重复恢复而多次递增，批次不会被重复整体生效。

### 不确定状态闭锁

批次写集自 BeginControl 起持久在 CONTROL 中。重启后、恢复完成前，写集内
实例的读、写、新批次一律以 `ErrInstanceUncertain` 拒绝——既不允许读到生效
过程中的中间值，也不允许在终态确定前提交新写入。恢复完成（RFinalize）后
解除。

### 四种终态

`COMMITTED` / `ROLLED_BACK` / `RECOVERED_UNDONE` / `RECOVERED_APPLIED`
分别持久化并可查询。对外可观察效果上，`RECOVERED_UNDONE ≡ ROLLED_BACK`、
`RECOVERED_APPLIED ≡ COMMITTED`（`TestRecoveredIndistinguishable` 逐字段
验证版本号、批次戳与属性完全一致）。

### 恢复扫描量的上界

恢复只读取两个东西：固定大小的 CONTROL，和 CONTROL 登记的那一个批次段。
批次干净结束后段文件即被删除，磁盘上不累积历史日志。因此扫描量 =
O（本批次记录数），与系统启动以来处理过的批次总数无关。证据不依赖任何额外
对外暴露的状态：`RecoverReport.JournalRecords/JournalBytes` 是恢复过程的
自然产出，`TestRecoveryScanBounded` 在处理 50 个历史批次后验证第 51 个批次
的恢复只扫描了 5 条记录（BEGIN+3×MUT+COMMIT），并用测试局部的磁盘读记录器
确认恢复期间只读取了本批次段文件。

## 关键取舍与被放弃的方案

### 选用：steal/no-force（ARIES 风格）+ 每批次独立日志段

变更在提交前即可落盘（steal），提交不强制刷数据（no-force）。这让"提交前
崩溃需要撤销、提交后崩溃需要重做"两条路径都被真实执行，也是最通用的形态。

### 放弃：no-steal/force（提交前集中刷写）

提交前不允许任何变更落盘、提交时强制全部落盘。撤销路径退化为空操作，无法
覆盖"生效过程中部分实例已变化"的场景；且提交点本身变成多记录原子写，反而
需要额外机制保证，违背"唯一判定时刻"的清晰性。

### 放弃：单一全局追加日志

恢复时需要扫描全局日志才能找到未决批次，扫描量随历史批次总数增长，直接违
背上界要求。按批次分段 + CONTROL 指针把扫描量限定在本批次规模。

### 放弃：影子页（shadow paging）

整库写时复制 + 根指针切换可以提供同样的原子性，但写放大随库规模增长，且
"部分生效"的中间态不可表达，无法支持 steal 语义下的撤销/重做验证；对本
场景的复杂度收益比过低。

### 放弃：批次内创建/删除实例

允许批次创建实例会引入墓碑（tombstone）语义，撤销时需要物理删除已落盘
记录，显著增加恢复分支。当前约束为：批量变更只作用于已存在实例（校验失败
即正常回退），单实例写入可创建实例。这是语义简化，不影响崩溃原子性结论。

## 本地验证方法

```bash
# 全量测试（含全中断点遍历、恢复再中断、随机对照）
go test ./ontology/

# 竞态检测 + 重复执行
go test -race -count=2 ./...

# 指定验证场景
go test -run TestCrashEveryPoint -v ./ontology/        # 每个中断点恢复后必归两种终态
go test -run TestRecoveryInterrupted -v ./ontology/    # 恢复过程自身再中断
go test -run TestRecoveryScanBounded -v ./ontology/    # 扫描量不随历史增长
go test -run TestRandomizedAgainstNaiveModel ./ontology/  # 随机序列 vs 朴素全量重放模型

# 演示程序
go run ./cmd/server
```

测试要点：

- `TestCrashEveryPoint`：在批次管线的**每一个**阶段之间逐一注入中断，恢复
  后状态必须精确等于"批次前状态"或"完整生效后状态"之一，无第三种结果；
- `TestRecoveryInterrupted`：对恢复管线的每一个阶段再注入中断，二次恢复
  必须收敛到同一终态；
- `TestRecoveryIdempotent`：同一中断重复恢复多次，终态与版本号不变；
- `TestRandomizedAgainstNaiveModel`：10 个随机种子 × 40 轮随机操作（正常
  提交/正常回退/随机点中断/恢复再中断 0~2 次），最终状态与独立实现的朴素
  全量重放模型逐字段一致；
- 每次中断的阶段、每次恢复的判定依据（COMMIT 记录存在与否）与归类结果都
  写入审计日志（JSON Lines），`assertAuditConsistent` 校验同一批次的多次
  判定必然一致。
