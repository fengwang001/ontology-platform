# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `ontology/`：脱敏与可见性策略冲突裁决引擎（设计说明见 [DESIGN.md](DESIGN.md)）。
  - 可见性策略与多条脱敏策略的确定性合并、冲突裁决与错误分类。
  - 原子快照保证运行期策略变更对单次呈现请求一致可见。
  - 按属性索引，单次呈现开销与系统中策略总数无关（`Result.Examined` 可观测）。
- `ontology/naive/`：独立维护的朴素参照实现，用于差分测试。
- `cmd/server/`：演示程序，输出呈现结果与 JSON 调用日志。

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
