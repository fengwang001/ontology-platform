# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `linkrepair/`：损坏快照链接记录修复裁决组件。按固定优先级区分
  结构损坏、引用不可用、基数冲突、重复记录四类异常，保证恢复结果
  满足链接类型声明的基数约束；纯函数、可并发重复调用、结果不漂移。
  设计说明见 `docs/DESIGN.md`，朴素参照模型与随机差分对照见
  `linkrepair/linkrepairtest/`。

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
