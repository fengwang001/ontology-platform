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

## 进程表（`proctable` 包）

`proctable` 实现一个带僵尸回收的进程表，容量为 N（存活与僵尸表项合计
不超过 N）。始进程编号为 1，建表时已存在且永不退出；新进程编号自 2 起
严格递增、不复用。所有方法（`Create` / `Exit` / `Wait` / `WaitAny` /
`Lookup` / `Children` / `Len` 等）均可并发调用，相同的操作序列重放得到
完全相同的表与回收顺序。

### 退出、改挂与回收规则

- 进程退出后成为僵尸，保留表项与退出码，直到被父进程等待回收。
- 退出瞬间，其全部子进程（存活的与僵尸的）改挂到始进程；改挂后归始
  进程的僵尸立即被回收、表项释放。
- 若退出者自己的父进程就是始进程，它退出后也立即被回收。
- `Wait(pid, child)` 回收指定的僵尸子进程并返回退出码；`WaitAny(pid)`
  回收子进程中最早成为僵尸者，无僵尸时区分「仍有存活子进程」
  （`ErrNoZombie`）与「没有任何子进程」（`ErrNoChildren`）。
- 不变量：任何时刻表项数 ≤ N；僵尸没有任何子进程；除始进程外每个表项
  的父进程存活；每个僵尸至多被回收一次。

### 错误与拒绝顺序

所有被拒绝的操作整体失败、不改变任何状态，原因可区分（`errors.Is`
判定）：`ErrNotExist`、`ErrZombie`、`ErrTableFull`、`ErrInitExit`、
`ErrNotChild`、`ErrChildAlive`、`ErrNoChildren`、`ErrNoZombie`。
多种原因同时成立时按「不存在、已是僵尸、其余」的固定顺序只报第一个，
例如父进程是僵尸且表已满时报 `ErrZombie`。

### 本地验证

```bash
# 运行进程表全部测试（日志含每步输入、输出与判定依据）
go test -v ./proctable

# 竞态检测
go test -race ./proctable
```
