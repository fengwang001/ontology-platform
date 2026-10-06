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

## 持久卷 / 卷声明绑定控制器（`pvbinding`）

`pvbinding/` 实现持久卷（PV）与卷声明（PVC）的绑定控制器，支持：

- 立即绑定：提交即选卷（最小容量、同容量名称字典序最小），新卷加入 /
  卷释放 / 卷修改后按声明名称升序重评估；选卷开销与无关存储类卷数无关。
- 延迟绑定：以 `(节点, 声明集合)` 发起全有或全无的联合指派，求总浪费最小、
  并列时卷名序列字典序最小的互不相同卷指派。
- 指定卷名称、预留声明、标签选择器、访问模式、节点约束等完整候选条件。
- 回收（`Retain` 停留 Released 不可复用、`Delete` 立即移除、管理员重置）。
- 已绑定声明扩容（只能增大且不超过卷容量）。
- 五类有优先级的错误、全量一致性自检 `CheckInvariants`、互斥锁线性化并发。

对外 API 见 `pvbinding/controller.go`（`New` / `AddVolume` / `UpdateVolume` /
`ResetVolume` / `AddClaim` / `DeleteClaim` / `ExpandClaim` / `JointBind` /
`Snapshot` / `Stats` / `CheckInvariants`）。设计取舍、被放弃方案与验证方法见
`pvbinding/DESIGN.md`。

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache
go test -race ./pvbinding/
go test -run TestRandomDifferential -v ./pvbinding/
```
