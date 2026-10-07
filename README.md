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

## 批量更新原子性与崩溃恢复

本体实例存储位于 `internal/store`，支持进程中断后安全恢复的原子批量更新：

- 每个批次走 `PREPARED → COMMITTED → COMMIT_DONE` 状态机；**唯一提交点**是
  `COMMITTED` 状态记录的耐久性边界，之前中断一律整体未生效，之后中断一律整体生效。
- 恢复幂等且可重入：恢复过程自身再次中断后重复恢复，终态不变、批次不会被重复生效。
- 不确定窗口内对涉及实例的读/写请求一律返回 `ErrUncertain`，不泄露中间属性。
- 恢复扫描量为 O(本批大小)，不随历史批次总数增长（`RecoveryReport.RecordsScanned` 为证据）。
- 四种可识别类别：`committed` / `aborted` / `recovered_committed` / `recovered_aborted`，
  后两者在实例状态上与前两者完全不可区分。

关键 API：`store.Open`（打开并恢复）、`Batch`（一步提交）、`Prepare` +
`CommitPrepared` / `Abort`（显式两阶段）、`Get` / `Put`。

设计取舍、被放弃方案与验证方法见 [`docs/design.md`](docs/design.md)。

故障注入与差分测试：

```bash
# 每个阶段边界逐一注入中断
go test -run TestExhaustiveBarriers ./internal/store -v

# 恢复过程自身再次被中断
go test -run TestRecoveryInterruptedDuringRecovery ./internal/store -v

# 随机序列 × 朴素全量重放模型对照（审计轨迹落盘）
go test -run TestRandomizedDifferential ./internal/store -v
cat internal/store/testdata/random-replay-audit.jsonl
```
