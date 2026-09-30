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

## STM（软件事务内存）

`stm` 包提供可组合的软件事务内存：多个执行体在共享整数变量（`TVar`）上
运行事务，支持阻塞重试（`Retry`）与「左分支不成则试右分支」的选择
（`OrElse`）。

### 基本用法

```go
s := stm.New()
a := s.NewTVar(100)
b := s.NewTVar(0)

err := s.Atomically(func(tx *stm.Txn) error {
    tx.Set(a, tx.Get(a)-50)
    tx.Set(b, tx.Get(b)+50)
    return nil // 正常返回即提交，全部写同时对外可见
})
```

### 一致时刻（快照语义）

- 事务体内 `Get` 读到的全部变量值取自同一个一致时刻：每次读都会在
  内部校验已读集合是否仍与共享内存一致；若已读值被其他事务的提交改写，
  本次执行的读写被丢弃并从头重新执行，因此任何一次执行（包括最终会被
  作废重跑的执行）都不会看到别的事务提交到一半的状态。
- 事务体内的写先缓冲在本地，提交前只对本事务可见（读己之写）；事务体
  正常返回时全部写原子地一次性对外可见。
- 事务体返回非 nil 错误或发生恐慌时事务中止、任何写都不生效，错误 /
  恐慌值原样上抛。

### 重试（Retry）

- `tx.Retry()` 返回一个哨兵错误，事务体须将其 `return` 才生效。
- 重试会丢弃本次执行的全部写，并阻塞到本次执行读过的某个变量被别的
  事务提交写入（写入相同值也算）后从头重新执行。
- 唤醒条件：提交发生时若读集中任一变量的版本与读取时不同即唤醒；
  写入发生在读之后、阻塞之前同样会唤醒（先校验再睡眠，不丢失唤醒）。
- 读集为空的重试永远无法被唤醒，立即以 `ErrBlockedForever` 中止。

### 选择（OrElse）

- `tx.OrElse(left, right)` 先执行左分支：左分支正常返回则不执行右分支。
- 左分支重试：撤销它的全部写，但它读过的变量仍计入本次读集；随后执行
  右分支，右分支看不到左分支的写。
- 两分支都重试：整体重试，读集为二者并集。
- 左分支返回错误或恐慌：整个事务中止，不试右分支。

### 误用检测

以下误用按此顺序只报第一个，事务中止且不产生任何写：

1. 事务结束后仍使用其句柄（`ErrTxnClosed`）
2. 在事务体内再启动事务（`ErrNested`）
3. 使用属于另一个内存实例的变量（`ErrForeignTVar`）
4. 一次执行访问的不同变量数超过 64（`ErrTooManyVars`）

### 本地验证

```bash
# 全部 STM 测试（含竞态检测与详细日志，日志含输入、输出与判定依据）
go test -race -v ./stm/

# 单个用例
go test -race -v -run TestTransferInvariant ./stm/
```
