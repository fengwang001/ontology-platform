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

## proctable：带僵尸回收的进程表

`proctable` 包实现容量为 N 的进程表（存活与僵尸合计不超过 N），
始进程编号为 1，建表时已存在且永不退出。所有操作（`Create`/`Exit`/
`Wait`/`WaitAny`/`Lookup`/`Children`/`Size`/`Reaped`）均并发安全：
在同一把互斥锁下串行化，因此相同的操作序列重放得到完全相同的表与回收顺序。

### 创建

- 父进程必须存活且表未满；新编号自 2 起严格递增、永不复用。

### 退出、改挂与回收

- 进程退出后成为僵尸，保留表项与退出码。
- 退出者的全部子进程（存活的与僵尸的）改挂到始进程；
  凡改挂后归始进程的僵尸立即被回收、表项释放。
- 若退出者自己的父进程就是始进程，它也立即被回收。
- 由此保证：任何时刻表项数不超过 N；僵尸没有任何子进程；
  除始进程外每个表项的父进程存活；每个僵尸至多被回收一次。

### 等待

- `Wait(parent, child)`：指定子进程为僵尸则回收并返回退出码。
- `WaitAny(parent)`：回收子进程中最早成为僵尸者，返回其编号与退出码；
  无僵尸时区分「仍有存活子进程」（`ErrNoZombie`）与「没有任何子进程」（`ErrNoChildren`）。

### 错误与判定顺序

被拒绝的操作整体拒绝、不改变任何状态，错误以 `*proctable.Error` 返回，
`Kind` 可区分原因：`ErrNotFound`、`ErrZombie`、`ErrTableFull`、`ErrInitExit`、
`ErrNotChild`、`ErrStillAlive`、`ErrNoChildren`、`ErrNoZombie`。
多因同时成立时按「不存在、已是僵尸、其余」的固定顺序只报第一个。

### 本地验证

```bash
# 全部测试（含竞态检测与输入/输出/判定依据日志）
go test -race -v ./proctable/

# 静态检查
go vet ./... && gofmt -l .
```

测试覆盖：父进程带着僵尸子进程退出时表项立即释放并可再创建、
父为始进程的退出即回收、等待任一按成为僵尸的先后、表满后经回收恢复创建、
多层改挂、错误优先级与拒绝原子性、并发安全与重放确定性。
