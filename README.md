# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## surge：区域运力调度与高峰加价

即时配送场景下按区域维护“在线可用运力 / 待派订单”供需关系的调度系统，位于 `surge/`：

- 档位状态机：上调即时生效、可跳多档；下调须连续确认且每次只降一档；等档清零计数。
- 订单创建即锁定档位，骑手按完成订单的锁定档结算补贴；晚到骑手（进入区域晚于下单）补贴豁免。
- 增量供需账本保证评估 O(1)，派单/完成不随平台订单总量增长。
- 全部操作在互斥锁下串行化，错误按固定次序报告且被拒操作不改状态。
- 详见 `surge/DESIGN.md`。

```bash
# 单元测试（含竞态检测）
go test -race -v ./surge

# 与朴素全量重算模型的随机差分（-v 打印每步输入/输出/判定依据）
go test -race -v -run TestNaiveDifferential ./surge

# 复杂度验证：1000 与 10000 规模耗时应基本相同
go test -bench . -run '^$' ./surge
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
