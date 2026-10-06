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

## 浅克隆边界与可达性（`shallow` 包）

`shallow` 包提供浅克隆仓库的边界管理与对象可达性服务，模块说明见
[`shallow/DESIGN.md`](shallow/DESIGN.md)。

```go
repo, _ := shallow.Load(remote, snapshot)          // 校验本地状态并载入
repo.Deepen(ctx, 3)                                 // 按深度深化（无副作用成功）
repo.DeepenSince(ctx, 1700000000)                   // 按时刻深化，路径独立停止
repo.Unshallow(ctx)                                 // 彻底去浅，边界变空
repo.IsCommitReachable("c1")                        // O(1) 单点可达性查询
repo.IsBlobReachable("b9")
res, _ := repo.GC()                                 // 回收，幂等；res 含数量与字节数
```

错误优先级（`errors.Is` 区分）：`ErrInvalidArg` > `ErrRefNotFound` >
`ErrRemoteMissing` > `ErrRemoteFetch` > `ErrIllegalState`；拉取中途失败
整体回滚。所有方法可安全并发调用，语义等价于某个串行顺序。

```bash
# 全量测试（含竞态检测、60 组随机图与朴素模型差分、20 组并发风暴）
go test -race -v ./shallow/
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
