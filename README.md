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

服务提供基数约束并发名额控制的最小 HTTP 演示：

- `POST /scopes`：注册作用域与基数上限
- `POST /links/begin`：两阶段新建关联的准入（携带 `observed_version`）
- `POST /links/heartbeat`、`/links/commit`、`/links/rollback`：进行中请求续期与终态
- `GET /scopes`：查询当前已确认/进行中数量与基线版本
- `GET /journal`：导出完整决策日志（到达时刻、进行中状态、判定依据、最终结果）

设计原理、关键取舍、被放弃的方案与需求逐条对照见 `docs/DESIGN.md`。

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 本任务关键用例
go test -run 'TestLastSlotContention|TestFailureReleaseWindow|TestLeaseExpiry' -v ./ontology
go test -run TestRandomizedEquivalence -v ./ontology   # 随机序列对照朴素串行模型 + 日志重放
go test -run TestMultiRoundContentionStress -v ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
