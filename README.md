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

## Pod 中断预算驱逐裁决服务（`disruption/`）

面向节点排空的 PDB 驱逐裁决：单驱逐、整批全有或全无、确认/取消、宽限
到期左闭回补、单调时钟、可程序化判断的错误类别（严格优先级）、并发可
串行化，以及“热路径开销不随无关 Pod/预算增长”的计数器结构证明。

- 设计与取舍：`docs/DESIGN.md`
- 实现：`disruption/state.go`（索引状态）、`disruption/service.go`（裁决）、
  `disruption/naive.go`（独立朴素参考模型）
- 随机差分对照：`go test -run TestRandomDifferential -v ./disruption`
- 复杂度证明：`go test -run TestCost -v ./disruption`
- 竞态检测：`go test -race ./...`
