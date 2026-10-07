# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 属性索引重建审计溯源证明器

`ontology/` 包实现“按值查找”属性索引的可审计重建：基准点用全局单调 LSN
（非墙钟）精确划分基线/增量；重建产出含逐条“值↔来源对象/来源 LSN”对应
关系的审计记录；复核器仅凭审计记录与对象当前状态即可逐条判定正确性，
失败重建的半成品索引永不对外可见。完整设计取舍见 [DESIGN.md](DESIGN.md)。

```bash
# 端到端演示：基线/增量、失败不可用、注入不一致复核、O(1) 取证
go run ./cmd/indexaudit 2>decision.log   # 人类结论在 stdout，判定 JSON 日志在 stderr
```

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
go test ./ontology/ontology
go test -run TestDifferential ./ontology/ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

关键测试：

- `TestBoundaryMembershipExact`：基线/增量边界恰好落在某次写入前后的归属。
- `TestFailedRebuildUnavailable`：重建中途失败后索引不可用，且与“不存在/未声明”区分。
- `TestIndependentVerifyDetectsTamper`：复核不依赖重建日志即可发现人为注入的条目级不一致。
- `TestConcurrentVerifiersConsistent`：并发写入/并发复核结论一致（配合 `-race`）。
- `TestDifferentialRandomInterleaving` / `TestDifferentialRandomFailures`：
   与独立朴素索引模型对照随机构造的写入/重建/失败交错。
- `TestSingleEntryVerifyConstantHistoryReads`：单条目复核历史读取数为常数（O(1) 取证）。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
