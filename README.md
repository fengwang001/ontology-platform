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

## STM：软件事务内存（`stm` 包）

多个执行体可在共享整数变量（`TVar`）上运行可组合的事务，支持阻塞
重试（`Retry`）与「左分支不成则试右分支」的选择（`OrElse`）。

```go
s := stm.New()
a, b := s.NewVar(100), s.NewVar(0)
_ = s.Atomically(func(tx *stm.Txn) error {
    av := tx.Read(a)
    if av < 10 {
        tx.Retry() // 阻塞到读过的变量被别的事务提交写入
    }
    tx.Write(a, av-10)
    tx.Write(b, tx.Read(b)+10)
    return nil
})
```

### 一致时刻

事务体执行期间读到的全部变量值取自同一个一致时刻：每次访问新变量
时都会在锁内校验整个读集（各变量版本自读取后未被提交写入），已失效
则丢弃本次执行的读写并从头重跑；提交前再校验一次，全部写在同一把锁
内同时生效，对外原子可见。因此任何一次执行（包括将被作废重跑的执行）
都不会看到别的事务提交到一半的状态，例如转账事务中账户和恒定。

### 重试（Retry）

- 丢弃本次执行的全部写，阻塞到**本次执行读过的某个变量**被别的事务
  提交写入后重新执行；写入相同值也算一次写入。
- 写入若发生在读之后、阻塞之前，版本已变化，阻塞前的校验会立即失败
  并重跑，不丢失唤醒。
- 读集为空的重试立即以 `ErrPermanentlyBlocked` 中止。

### 选择（OrElse）

- 先执行左分支：左分支正常返回则不执行右分支。
- 左分支重试：撤销它的全部写，但它读过的变量仍计入本次读集；再执行
  右分支，右分支看不到左分支的写。
- 两分支都重试：整体重试，读集为二者并集。
- 左分支返回错误或恐慌：整个事务中止，不试右分支。

### 中止与误用

事务体返回错误或发生恐慌：事务中止、任何写都不生效，错误原样返回、
恐慌原样上抛。以下误用按此顺序只报第一个并使事务中止（不产生任何写）：

1. 事务结束后仍使用其句柄（`ErrTxnDone`）
2. 在事务体内再启动事务（`ErrNested`）
3. 使用属于另一个内存实例的变量（`ErrForeignVar`）
4. 一次执行访问的不同变量数超过 64（`ErrTooManyVars`）

### 唤醒条件

被 `Retry` 阻塞的事务在其读集中任一变量被其它事务**提交写入**时唤醒
（写入相同值也唤醒）；校验与等待在同一把锁上进行，提交时广播，因此
不丢失唤醒。相同的顺序执行得到相同结果。

### 本地验证

```bash
# 全部测试（含转账不变量、生产者/消费者阻塞重试、OrElse、误用等）
go test -race -v ./stm

# 日志会打印每个用例的输入、输出与判定依据
go test -race -count=3 ./stm   # 反复运行以验证并发稳定性
```
