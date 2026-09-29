# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 物化视图幂等重建器（`mview` 包）

`mview.Rebuilder` 将一个有序源序列按序计数派生为物化视图（值 -> 出现次数），
支持分块重建、断点续跑，以及重建全程旧视图可读、完成时原子切换。

### 分块与检查点

- `BeginRebuild(source, chunkSize)` 以固定块大小（必须 `>= 1`）开始重建；
  重建结果写入对读者不可见的影子副本，旧视图与代数保持不变。
- `Step()` 处理下一块：整块先在局部缓冲中计数，整块成功后才一次性并入
  影子副本并推进已处理数，这一步即“检查点”。因此检查点只落在块边界。
- `Crash()` 模拟块之间崩溃：当前未完成块不留下任何半成品，影子副本与
  已处理数停在上一检查点；下一次 `Step()` 自动从上一检查点继续，
  已处理过的块不会重复计数。崩溃后、恢复前不允许提交。
- `Step()` 在全部块处理完后返回 `false`，重复调用不改变状态。

### 原子切换与代数

- 重建期间 `Snapshot()` 始终返回旧视图与旧代数（首个视图为第 1 代）。
- 全部块处理完后 `Commit()` 在同一把锁内完成“视图指针切换 + 代数递增”，
  并发读者要么看到完整旧视图（旧代数），要么看到完整新视图（新代数），
  绝不会读到影子半成品。

### 拒绝原因（整体拒绝，失败不改状态）

| 场景 | 错误（`errors.Is` 可判定） |
| --- | --- |
| 块大小 `< 1` | `ErrInvalidChunkSize` |
| 已在重建（含崩溃待恢复）时再次 `BeginRebuild` | `ErrRebuildInProgress` |
| 未在重建时 `Step` / `Commit` / `Crash` | `ErrNoRebuild` |
| 未处理完（含崩溃后未恢复）就 `Commit` | `ErrRebuildIncomplete` |

任何一次失败都不会修改已提交视图、代数、影子内容或已处理数；例如
“未完成就提交”被拒后仍可继续 `Step` 直至完成再提交。

### 本地验证：与从头重放核对

“从头重放”基准由 `mview.Replay(source)` 给出：对完整源序列按序计数一次。
任何分块大小、任意块之间崩溃后的续跑结果，提交后都必须与
`Replay(source)` 深度相等，且代数恰好在提交时加一。

```bash
# 竞态检测 + 详细日志（操作、已处理数、影子内容、视图与判定依据）
go test -race -v ./mview

# 关注的核心场景
go test -run 'TestCrashResume|TestOldViewReadableDuringRebuild|TestRejectionPaths|TestConcurrentReads' -v ./mview
```

关键测试：

- `TestCrashResume`：块之间崩溃后续跑，结果等于 `Replay` 与全新一次性重放。
- `TestOldViewReadableDuringRebuild`：重建每一步读到的都是旧视图/旧代数。
- `TestRejectionPaths`：四类拒绝原因及“失败不改变状态”。
- `TestReplayEquivalenceAcrossChunkSizes`：块大小 1/2/3/7/100 且每隔一块
  崩溃一次，结果全部等于从头重放（始终可复现）。
- `TestConcurrentReads`：8 个并发读者在重建与提交期间只能读到完整的
  旧视图或新视图。
