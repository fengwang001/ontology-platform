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

## TicToc 乐观事务验证器（`tictoc` 包）

`tictoc` 包实现了提交时才推导时间戳的数据驱动乐观事务（TicToc）。每个元组保存值 `v`、写时间戳 `w`、读时间戳 `r`（恒有 `w <= r`）和至多一个锁持有者；事务号由 `Begin()` 从 1 起严格递增。

### 调用接口

- `NewDB(k)`：构造 `k` 个元组（编号 `0..k-1`），`k` 不在 `[1,64]` 返回 `ErrInvalidConfig`，整体拒绝。
- `Begin()`：返回新事务号，事务为活跃态。
- `Read(t,k)`：本事务写过则返回缓冲值；否则读过则返回首次读到的值（读集不刷新）；否则返回元组当前值并记录 `(k,w,r)`。他人持锁不阻塞读取。
- `Write(t,k,x)`：仅写入事务缓冲，同键后写覆盖先写，不要求先读。
- `Prepare(t)`：见下，成功后事务为已准备态并持有写集锁，返回提交时间戳 `c`。
- `Finish(t)`：安装写集（`v=缓冲值, w=r=c`）并解锁，事务转已提交，返回 `c`。
- `Abort(t)`：活跃或已准备事务释放写集锁并转已中止。

### 提交时间戳的推导（Prepare 第②步）

```
c = max( 写集各元组当前 r + 1 的最大值, 读集各记录首次读到的 w 的最大值 )
```

读写集皆空时 `c = 0`。因此只看到过时间戳 0 的只读事务会以 `c=0` 提交，排在已先 Finish 的写入事务之前。

### 延拓条件（Prepare 第③步，按元组编号升序）

对每条读集记录 `(k,w0,r0)`：

1. `r0 >= c`：直接通过，不再读取元组（`r0` 恰好等于 `c` 即走此快路径）。
2. 否则元组当前 `w != w0`：以版本变化中止。
3. 否则当前 `r >= c`：通过。
4. 否则当前锁持有者是他人：以延拓受阻中止；是自己（该键也在写集中）：通过且不改 `r`；无人持锁：把 `r` 延拓为 `c`。

### 中止原因与回滚规则

三种中止原因分别是 `ErrLockConflict`（第①步升序加写集锁时遇他人持锁）、`ErrVersionChanged`、`ErrExtensionBlocked`，可经 `errors.Is` 精确区分。任一步中止都撤销本次调用已加的全部锁与已做的全部 `r` 延拓，元组逐字段恢复为调用前状态，事务转已中止。已提交/已中止事务对任何调用都返回 `ErrWrongState`，状态不再变化。

调用被拒绝时按“事务号不存在 → 状态不符 → 元组编号越界（仅 Read/Write）”的顺序只报第一个原因，且不改变任何状态。

### 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素模拟对照）
go test ./tictoc/ -v

# 竞态检测下运行（含并发安全用例）
go test -race -count=1 ./...
```

测试内容：两个规格内算例、只读事务以更小时间戳提交、`r+1` 与读到的 `w` 取大、`r0==c` 快路径与 `r0==c-1` 才核对、三种中止原因各一例、持锁下 `r0>=c` 仍通过、自己持锁的读写键不延拓、中止撤销延拓与锁、重复读返回首次值、拒绝原因优先级与纯性、触及次数不超过 `2*(|读集|+|写集|)`、并发安全；`TestNaiveDifferential2000` 用独立逐行照抄规则的朴素模拟器对 2000 组随机调用序列逐条比对返回、元组状态、锁/时间戳不变量、触及预算，并按 `(c, Finish 先后)` 串行重放校验每个事务读到的值；`TestDeterministicReplay` 验证相同调用序列产生完全相同的结果与状态。`-v` 日志打印每步输入、输出与判定依据。
