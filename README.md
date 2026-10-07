# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统

- `ontology/`：变更流消费与补偿子系统。消费动作变更事件，对每条
  动作成功事件恰好触发一次原子补偿动作；支持重复投递去重、崩溃
  后续作、撤销语义、错误分类与决策审计。设计说明见
  [docs/DESIGN.md](docs/DESIGN.md)。

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
