# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `flights/`：航班延误连锁调整引擎。给定计划航班表（每段绑定飞机与机组），
  沿飞机链与机组链推算实际起降时刻、推迟/取消结论与取消原因；
  支持延误/取消的多次注入与撤回、时钟单调校验、已起飞航班冻结、
  并发调用等价串行。设计取舍见 `flights/DESIGN.md`。

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
