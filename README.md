# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

本仓库实现了链接类型的**单侧基数约束**以及其在**乐观提交 + 有限次重试**下的一致性保证：
每次重试都基于最新版本与关联关系重新校验，区分版本冲突 / 基数拒绝 / 重试耗尽三类互斥原因，
判定开销与现有链接总数无关，并由朴素串行参考模型做随机对照验证。
详见 [`docs/DESIGN.md`](docs/DESIGN.md)。

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

# O(1) 基数判定基准（N=100 与 N=10000 同量级）
go test -run NONE -bench=. ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## HTTP 接口

`POST /update`（乐观写入）：

```json
{
  "instance": "x",
  "baseline": 0,
  "ops": [{ "typeId": "owns", "side": "A", "other": "y1", "add": true }]
}
```

响应 `200` 成功，或 `422`（`CARDINALITY_VIOLATION`）、`503`（`RETRIES_EXHAUSTED`）。
响应体的 `attempts` 数组完整记录每次内部尝试读到的版本、链接快照与逐约束判定依据，可重放核验。
