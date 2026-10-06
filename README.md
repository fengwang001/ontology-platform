# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 快递智能柜（`locker` 包）

`locker/` 实现了格口寄存与取件系统：投递员存件、收件人取件（取件码+手机号后四位）、
滞留费缴纳、超时判定与运营回收/解锁。

- 设计、取舍与被放弃方案：[`locker/DESIGN.md`](locker/DESIGN.md)
- 格口分配用三级层次位图，每次分配 ≤ 9 次机器字访问，探测数经 `Stats()` 可观测
- 拒绝优先级、冷却边界、错误计数例外等均有单测；另以独立朴素模型做 12 万次随机差分
- 业务闭环演示：`go run ./cmd/locker-demo`（打印每个操作的输入、输出与判定依据）

```bash
PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache go test -race ./...
PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache go run ./cmd/locker-demo
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
