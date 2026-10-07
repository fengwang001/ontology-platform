# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统

- `lifecycle/`：对象生命周期状态机子系统。到期迁移采用惰性结算（无后台扫描），
  支持多步到期链、到期与显式动作同调用的先后次序、跨实例链式联动、时钟回退、
  并发去重、按历史重放任意时刻状态，并附带独立朴素扫描模型做随机对拍。
  设计取舍与被放弃方案见 `lifecycle/DESIGN.md`。

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
