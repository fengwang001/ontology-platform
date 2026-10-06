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

## delivery 包：配送异常上报与无法送达处置

`delivery/` 实现骑手送达异常（联系不上 / 地址有误 / 用户拒收）上报、
窗口判定与无法送达后的退回或就地结算，时刻为全局单调整数秒。

- 设计与取舍：`delivery/DESIGN.md`
- 朴素对照模型：`delivery/naive.go`（事件日志重放，独立于惰性实现）
- 随机差分与逐步日志：`go test ./delivery/ -run TestRandomDifferential -v`
- 复杂度验证基准：`go test ./delivery/ -bench=. -run=^$`
- 竞态与全量：`go test -race ./...`

## 代码检查

```bash
gofmt -l .
go vet ./...
```
