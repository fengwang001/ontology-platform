# 批量更新原子性与崩溃恢复设计说明

## 1. 目标与终态

一次批量更新无论在何处被进程中断打断，重新打开后只能落到两种终态之一：

- **整批完全生效**：批次涉及实例的版本号、属性与批次完整生效后的结果逐字节一致；
- **整批完全未生效**：相关实例与批次发起前逐字节一致，等价于批次从未发生。

禁止第三种状态，禁止同一批次被整体生效超过一次。对外还必须区分四种记录类别：

| 类别 | 触发情形 | 实例可观察效果 |
| --- | --- | --- |
| `committed` | 正常提交 | 完整生效 |
| `aborted` | 提交点之前的正常回退 | 与未发起一致 |
| `recovered_committed` | 中断后恢复到已生效终态 | 与 `committed` 不可区分 |
| `recovered_aborted` | 中断后恢复到未生效终态 | 与 `aborted` 不可区分 |

后两者只在审计/恢复报告中单独识别，实例状态与对应正常情形完全相同。

## 2. 唯一判定时刻（commit point）

状态机为每个批次维护一个持久状态标记：

`PREPARED → COMMITTED → COMMIT_DONE`（回退路径为 `ABORTED → ABORT_DONE`）。

**唯一的“已被外部认定为已生效”时刻，是 `COMMITTED` 状态记录成功持久化（`Put` + fsync/原子替换）返回的那一耐久性边界（barrier）。**

- 该边界之前的任意中断：恢复一律回退到未生效；
- 该边界处或之后的任意中断：恢复一律前滚到完全生效。

这一判定只读取该批次自身的两个固定记录（活动指针 + 批次状态标记），不依赖时间、锁、其他进程或内存状态，因此确定且可复现：对同一份崩溃磁盘重复恢复、或从同一磁盘的独立克隆分别恢复，归类结果相同（见 `TestRecoveryDeterministic`）。

## 3. 存储布局

键值底座只要求四种原语：`Get` / `Put`（每次都是耐久性边界）/ `Delete` / `Rename`。两种实现：

- `FileEngine`：一键一文件；`Put` 写临时文件 → `fsync` → 原子 `rename` → 目录 `fsync`，真实掉电安全；
- `MemEngine` + `MemDisk`：进程死亡只丢失内存态，重开看到的是最后一次持久化完成的快照，用于确定性故障注入；`MemDisk.Clone()` 支持“同一崩溃态恢复两次”的对照。

键布局（ID 做十六进制编码以兼容文件名）：

```
active                  单个在途批次指针（仅内部指针，非对外状态）
b/<batch>/active        批次状态机标记（PREPARED/COMMITTED/COMMIT_DONE/ABORTED/ABORT_DONE）
b/<batch>/i/<seq>       每个实例一条已准备 intent（旧/新版本+旧/新属性）
i/<object>              实例当前值（版本号 + 属性）
j/<seq>                 追加式审计日志（每事件独立记录，不参与恢复判定）
```

## 4. 正常提交流程（阶段与耐久性边界）

1. **准备（prepare）**
   - 读取每个涉及实例的前像，构造 intent（旧版本、新版本=旧版本+1、旧属性、新属性）；
   - 逐条持久化 intent（`intent.write.N`）；
   - 持久化活动指针（`active.pointer.write`），内存中把涉及对象标记为“不确定”；
   - 持久化 `PREPARED`（`state.prepared.write`）。
   - 此阶段不修改任何实例值。
2. **提交点**：持久化 `COMMITTED`（`state.committed.write`）。这是唯一判定时刻，随后写审计 `commit_point`。
3. **前滚安装**：按 intent 顺序写实例新值（`instance.apply.N`）。每条安装都是**幂等**的——直接写入该 intent 的目标版本/属性，不做“读当前值再递增”，因此重复恢复不会二次加版本。
4. **完成与清理**：写 `COMMIT_DONE` → 删除 intent（`intent.cleanup.N`）→ **最后**清空活动指针（`active.pointer.clear`）→ 审计 `done`。

## 5. 正常回退（提交点之前）

`AbortPrepared`：逐条核对实例仍等于各自前像（窗口内写入已被拒绝，正常必成立），写 `ABORT_DONE`，删除 intent，最后清指针，审计记为 `aborted`。实例自始至终未被修改。

## 6. 恢复算法（幂等、可重入）

`Open` 时：

1. 读取 `active`。不存在或为空 → 无在途批次，直接结束（**O(1)**）。
2. 读取 `b/<batch>/active`：
   - 状态标记不存在（指针在、标记未到）或为 `PREPARED` → 归类 `recovered_aborted`，走回退；
   - 为 `COMMITTED` → 归类 `recovered_committed`，幂等前滚安装；
   - 为 `COMMIT_DONE` / `ABORT_DONE` → 终态已达成，仅重做剩余清理，归类沿用终态；
3. 恢复自身在任何边界再次中断都安全：所有安装/删除/清指针操作可任意重复，重复归类结果不变，批次最多整体生效一次。

关键顺序取舍：**intent 先删、活动指针最后清**。这样清理中途崩溃后，恢复仍能凭指针命名该批次、按状态标记重做幂等清理；若先清指针再删 intent，可能出现“状态标记与残留 intent 无法被再次命名”的窗口（开发中实测到该缺陷并据此修正）。

## 7. 不确定窗口

从活动指针持久化（对象进入在途集合）到批次终态确定期间：

- 对涉及实例的 `Get` 返回 `ErrUncertain`，绝不对外暴露 `instance.apply` 过程中的中间属性；
- 对涉及实例的外部 `Put` 返回 `ErrUncertain`；不涉及的实例读写不受影响；
- 进程已死亡但尚未恢复时，原始底座上实例值只可能是前像（提交点之前）或被幂等安装为完整新值（之后），不存在中间值可读。

同一时刻只允许一个在途批次（`ErrBusy`）。

## 8. 恢复扫描量有界（不随历史批次数增长）

恢复只跟随 `active` 这一个指针读取**当前这一个**批次的固定两条记录加上它自己的 N 条 intent：扫描量 `O(N)`，N = 本次批涉及实例数，与系统自启动以来处理过的批次总数无关。证据：

- `RecoveryReport.RecordsScanned` 由恢复过程直接计数，测试断言其上界 `2 + 批大小`；
- `TestBoundedRecoveryIndependentOfHistory` 先完成 50 个历史批次，再中断一个 3 实例批次，断言扫描量恒为 5，不随 50 增长；
- 判定不读取 `j/` 审计日志（日志仅作重放核验），不依赖额外对外暴露状态。

## 9. 被放弃 / 被否决的方案

- **后台异步补偿 / “最终一致”修复**：中断后、补偿完成前会对外暴露中间态，违反“只有两种终态”，否决。
- **以内存锁或时间戳作为提交点**：崩溃后锁丢失、时钟不可复现，无法确定性归类，否决。
- **安装时“读当前版本再 +1”**：前滚重复执行会重复加版本，违反“只整体生效一次”；改为按 intent 写入绝对目标版本（幂等赋值）。
- **先清活动指针再删 intent**：清理中途崩溃会留下无法再次命名的残留（实测），改为先删 intent、最后清指针。
- **全局重放所有历史批次日志来恢复**：扫描量随历史增长，违反有界恢复要求；改为单个活动指针 + 批次私有记录。
- **把审计日志作为恢复输入**：会把“核验证据”和“判定依据”耦合；日志只追加、只用于重放核验，判定仅依赖状态标记。

## 10. 本地验证方法

```bash
go test ./...                         # 全量
go test -race -v ./...                # 竞态 + 详细
go test -run TestExhaustiveBarriers ./internal/store -v   # 逐中断点
go test -run TestRecoveryInterruptedDuringRecovery ./internal/store -v
go test -run TestRandomizedDifferential ./internal/store -v
go vet ./... && gofmt -l .
```

测试覆盖：

- `TestExhaustiveBarriers`：在三实例批次的每个阶段边界逐一注入中断，断言恢复后只落入两种终态，并与朴素模型逐实例比对；
- `TestRecoveryInterruptedDuringRecovery`：首次中断后，在恢复过程的每个可达边界再次注入中断，再恢复、再三重开，验证收敛与版本冻结；
- `TestRandomizedDifferential`：120 个固定种子、随机批量更新 × 随机首次中断 × 最多两次恢复期再中断，全量对照独立朴素重放模型；每次的种子、各批次操作、首次中断阶段、恢复期中断阶段、最终归类都落盘到 `internal/store/testdata/random-replay-audit.jsonl`，可据此重放；
- `TestRecoveryDeterministic`：同一崩溃磁盘两份独立克隆恢复归类必须一致；
- `TestFileEngineCrashReplay`：在真实 `fsync` + 原子 rename 的文件引擎上验证重启恢复；
- `TestUncertaintyWindowRejectsTraffic`、`TestBoundedRecoveryIndependentOfHistory`：不确定窗口拒绝与恢复有界性证据。

朴素模型（`model_test.go`）是独立实现：规则只有一句——中断严格早于唯一提交点则批次“从未发生”，处于或晚于该点则“整体发生”；每次生效对每个涉及实例绝对地写入新属性并版本号 +1。生产实现经状态机达到相同结果，两者在随机差分测试中必须逐实例一致。
