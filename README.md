# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `compat/`：快照格式兼容性校验组件。在某一版本格式的消费方读取另一版本
  格式产出的快照时，依据对象类型结构变化（属性增删改与必填性变化）判定
  快照对该消费方可读、需降级或必须拒绝。设计说明见
  [docs/snapshot-compat-design.md](docs/snapshot-compat-design.md)。

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
