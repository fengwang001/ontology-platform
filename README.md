# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- [`reachability/`](./reachability/)：有向图可达性增量维护组件。边带重数、
  允许自环；随增删边实时维护全量可达点对，删边时“先移除可能受影响点对、
  再朴素遍历重新推导加回”，并支持并发一致读取。语义、加删边规则与 API
  详见 [`reachability/README.md`](./reachability/README.md)。

## 环境要求

- Go 1.26+（`go version` 确认）

## 本地验证

```bash
# 拉取依赖
go mod tidy

# 全量测试
go test ./...

# 带竞态检测与详细输出（推荐，覆盖并发增删读取）
go test -race -v ./...

# 单个包 / 单个用例
go test ./reachability
go test -run TestDeleteEdgeOnCycle ./reachability

# 多轮重复执行（验证确定性：同一输入序列输出完全一致）
go test -count=10 ./reachability

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

测试覆盖：删环上的边（先移除后恢复）、重复加边后只删一次、自环增删、
桥边删除、快照隔离、各类非法输入（空节点名 / 数量为 0 / 删除不存在的边 /
重数超限）、随机增删与朴素遍历逐步比对、并发读写竞态检测，以及
同序列重放的确定性比对。开启 `WithLogger` 后日志会打印每次操作的输入、
可达集合与判定依据（见证路径）。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
