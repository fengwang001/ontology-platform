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

## 顺路合并派单（dispatch 包）

`dispatch/` 实现骑手在途路线的新单合并：插入可行性、确定性骑手/位置选择、
停靠完成推定、取消收缩、单调时钟与并发协调。耗时由外部 `TravelTimeSource`
注入（不假设三角不等式），无可行骑手时返回三种可程序化区分的错误：

- `ErrNoRiderRegionCapacity`：同区骑手全部满载；
- `ErrNewOrderPromise`：有余量但没有位置满足新单承诺；
- `ErrExistingViolation`：能满足新单但会让在途订单超承诺或超单次绕路上限。

关键设计与被放弃方案见 `dispatch/DESIGN.md`；随机差分对照（主实现 vs 独立穷举朴素模型，
含逐步输入/输出/判定依据日志）见 `dispatch/differential_test.go`。

```bash
go test -race -v ./dispatch/
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
