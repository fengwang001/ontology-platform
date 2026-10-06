# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 充电站功率分配与排队控制器（`charger` 包）

事件驱动的充电站模拟：可变总功率上限、按接口/车辆上限的整数分水填充、
最低可用功率不达标即排队等待、优先类别严格先于普通类别、充满自动结算并触发重分。
设计见 `charger/DESIGN.md`。

```bash
go test ./charger/...      # 包内分配/时钟单元测试
go test -run TestRandomDifferential -v   # 与逐秒朴素模型的随机差分（含日志）
go test -race ./...        # 并发安全
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
