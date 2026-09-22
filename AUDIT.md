# AUDIT：不变量、保证位置与钉住测试

## 第二节 五条不变量

1. 补偿严格逆序且只补偿已成功步骤
   - 保证位置：`compens/compens.go Build` 从 `len(steps)-1..0` 扫描，只把 `succeeded[i]`
         放入 Items，其余进 Skipped；`saga.go runCompensate` 只执行该计划。失败步无
     forwardSuccess 记录，天然不在 succeeded 中。
   - 钉住测试：`compens.TestBuildPlan`、`saga.TestReverseCompensation`（断言 c 未补偿、顺序 b→a）。
2. 补偿失败不吞掉，状态可区分且能列出失败步骤
   - 保证位置：`saga.go runCompensate` 收集 failure 后继续循环；`state.go reconstruct`
     汇总 CompensateFailures 并给出 Compensated/CompensateFailed 两态。
   - 钉住测试：`saga.TestCompensationFailureContinues`。
3. 重放幂等（动作真实调用总次数跨 Resume 不变）
   - 保证位置：`drive` 只从「最后成功之后」推进；`runCompensate` 跳过已有
     compensateSuccess 的步骤；调用计数只在 `attempt` 真实调用动作时 +1。
   - 钉住测试：`saga.TestResumeIdempotent`、`saga.TestResumeHappyPathNoop`、
     `saga.TestRetryRules`。
4. 日志是唯一事实来源
   - 保证位置：`state.go reconstruct` 是唯一归约函数；在线 `snapshotLocked` 与离线
     `Reconstruct` 都调用它；`SelfCheck` 用 reflect.DeepEqual 比对二者。
   - 钉住测试：`saga.TestRebuildMatchesOnline`、`saga.TestSelfCheck`（编写中）。
5. 实例间完全隔离
   - 保证位置：每个实例独立 `inst`（独立 steps/calls/mu），日志记录带 InstanceID 过滤。
   - 钉住测试：`saga.TestConcurrentInstances`（编写中）。

## 第四节 Resume 读取条数实测

| 日志长度 | Resume 读取记录数 | 测试 |
| --- | --- | --- |
| 100 | 1（仅 Last 末条） | `saga.TestResumeReadsConstantInLogLength/100_records` |
| 10000 | 1（仅 Last 末条） | `saga.TestResumeReadsConstantInLogLength/10000_records` |

（测试运行后回填实测值；计数器是非导出字段 saga.resumeReads。）
