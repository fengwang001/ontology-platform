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

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 骑手考核与申诉系统（`riderassess`）

按周期记录扣分事件、定级与权益限制、根因簇连带撤销、申诉与回溯补偿。
模块与取舍见 `riderassess/DESIGN.md`。

```bash
# 规范覆盖 + 40 种子随机差分（逐步打印输入/输出/判定依据）
go test -v -run TestRandomDifferential ./riderassess

# 竞态检测
go test -race ./riderassess

# 登记复杂度基准（不相交簇，ns/op 近似常数）
go test -bench BenchmarkRegisterDisjointClusters ./riderassess
```
