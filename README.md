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

## freight：承运商运价合同运费计算与结算

- 实现：`freight/`（合同管理、计价引擎、结算、多承运商询价）
- 设计说明（关键取舍、被放弃的方案、验证方法）：`docs/freight-design.md`
- 测试：`go test ./... -race`；随机对照日志 `go test ./freight/ -run TestRandomOps -v`
- 复杂度基准：`go test ./freight/ -run xxx -bench .`
