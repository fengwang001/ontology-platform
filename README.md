# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 厢式货车配载与卸货顺序校验

`van` 包实现多停靠点货车的配载与卸货顺序校验：在顺序（后卸靠前）、危险品隔离、
载重、容积四类约束同时成立时，把货物放入编号最小的可行分区；失败时给出按严重度
（顺序冲突 > 隔离冲突 > 超重 > 超容）归并的可区分原因；卸货按停靠点递增推进，
支持中途装货、批量全有或全无与并发一致快照。

- 核心实现：`van/`（类型、分区聚合、放置判定、并发服务）
- 独立朴素参照模型与随机差分：`model/`
- 命令行演示：`cmd/demo`
- 设计说明（取舍、被放弃方案、顺序判定复杂度证明、本地验证）：`docs/design.md`

```bash
go run ./cmd/demo
DIFF_LOG=logs/diff.log go test ./model/ -run TestDifferentialRandom
go test -run '^$' -bench BenchmarkChoose ./van/
```

## 环境要求

- Go 1.26+（`go version` 确认）

若 `go` 不在 PATH，可 `export PATH=$PATH:/usr/local/go/bin`；
若构建缓存目录只读，可 `export GOCACHE=/tmp/gocache`。

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
