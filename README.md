# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- [`presence/`](presence/): 多设备在线状态聚合与订阅通知服务。设备租约惰性
  到期、按观察者独立计算隐身/拉黑可见性、通知按生效时刻与操作到达序精确合流。
  设计与取舍见 [`presence/DESIGN.md`](presence/DESIGN.md)。

```bash
# 单元测试 + 1500 组随机序列与独立朴素模型对照（可落输入/输出/判定日志）
go test ./presence/
PRESENCE_LOG=/tmp/presence.log PRESENCE_RUNS=1500 \
  go test -run TestNaiveDifferential -v ./presence/

# 竞态检测与复杂度基准
go test -race ./presence/
go test -bench=. -benchmem -run=^$ ./presence/
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
