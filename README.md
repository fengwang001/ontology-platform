# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## TicToc 乐观事务验证器（`tictoc` 包）

`tictoc` 实现了 TicToc 式数据驱动时间戳的乐观事务验证器：事务在
`Prepare` 时才由读写集推导提交时间戳、按需延拓所读版本的有效期并
在 `Finish` 时安装写入。

### 数据模型

- 构造参数为元组数 `K`（1 到 64，编号 `0..K-1`），越界时 `New`
  整体拒绝（`ErrInvalidK`）。
- 每个元组为 `(v, w, r, lock)`：值 `v`（int64，初值 0）、写时间戳
  `w`、读时间戳 `r`（初值均为 0，恒有 `w <= r`）、锁持有者 `lock`
  （初值无）。被锁元组恰是已准备事务的写集。
- `Begin()` 返回从 1 起严格递增的事务号，新事务为活跃态。

### 提交时间戳的推导

`Prepare(t)` 依次四步：

1. **加锁**：按元组编号升序给写集逐个加锁，遇被他人持有则以锁冲突
   中止（`ErrAbortLockConflict`）。
2. **推导**：提交时间戳 `c = max(写集每个元组当前 r + 1, 读集每条
   记录读到的 w)`；读写集皆空时 `c = 0`。因此 `Finish` 安装的 `w`
   严格大于该元组原 `r`。
3. **核对**：按元组编号升序逐条核对读集记录 `(k, w0, r0)`：
   - `r0 >= c`：通过，不再看元组；
   - 否则元组当前 `w != w0`：以版本变化中止
     （`ErrAbortVersionChanged`）；
   - 否则当前 `r >= c`：通过；
   - 否则当前锁持有者是他人：以延拓受阻中止
     （`ErrAbortExtendBlocked`）；是 `t` 自己（读写同键）则通过且
     不改 `r`；无人持有则把元组 `r` **延拓**为 `c`。
4. **成功**：`t` 转为已准备并返回 `c`，此后持有写集的锁直到
   `Finish` 或 `Abort`。

`Finish(t)` 把写集每个元组置为 `(缓冲值, c, c)` 并解锁，事务转已
提交并返回 `c`。`Abort(t)` 对活跃或已准备的事务释放其锁并转已
中止。

### 回滚规则

`Prepare` 任一步中止都回滚本次调用的全部加锁与延拓，使所有元组与
调用前逐字段相同，事务转已中止。被拒绝的调用（事务号不存在
`ErrTxnNotFound`、状态不符 `ErrInvalidState`、元组编号越界
`ErrTupleOutOfRange`，按此顺序只报第一个）不得改变任何状态；已
中止与已提交的事务不可再被任何调用改变。

### 正确性性质

- 所有调用可并发执行，结果等价于某个串行顺序（实现以单个互斥锁
  串行化每次调用）。
- 把全部已提交事务按 `(c, Finish 先后)` 串行重放，每个事务读到的
  值都等于重放时的值（随机对照测试中逐序列验证）。
- 相同调用序列重放得到完全相同的结果与元组状态。
- `Prepare` 触及元组的次数不超过 `2 * (读集大小 + 写集大小)`
  （非导出计数器 `prepareTouches`，测试中校验）。

### 本地验证

```bash
# 确定性单测（含三种中止原因、延拓回滚、重复读、边界 r0==c 等）
go test ./tictoc

# 2000 组随机调用序列与朴素模拟逐步对照，日志打印输入/输出/判定依据
go test -v -run TestRandomSequencesMatchModel ./tictoc

# 竞态检测（含并发冒烟测试）
go test -race ./tictoc
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
