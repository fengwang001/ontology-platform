# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 骑手考核与申诉系统

实现位于 `rider/` 包：固定周期扣分定级、根因簇连带撤销、申诉窗口与已结算周期回溯补偿。
设计取舍见 `rider/DESIGN.md`；测试含独立全量重算朴素模型与随机差分对照：

```bash
# 全量（含竞态检测）
go test -race ./...

# 查看差分测试逐步输入/输出/判定依据
go test -run TestDifferential -v ./rider
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
