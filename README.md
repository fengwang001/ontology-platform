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

## 变更流消费位点两阶段提交器（`twophase`）

`twophase` 包实现“先写外部副作用、后进消费位点”的两阶段原子提交，
保证崩溃重启后**效果不丢**、位点**连续不跳**、操作**可复现**。

### 两阶段顺序

每条事件按连续递增序号（从 1 开始）投递：

1. **阶段一 Prepare —— `Write(seq, action)`**：副作用先持久化（fsync），
   此时位点**不变**。只有 `seq == 已提交位点 + 1` 才允许写入。
2. **阶段二 Commit —— `Commit(seq)`**：仅当 `seq == 已提交位点 + 1`
   **且**该序号的副作用已持久化时，位点才推进一格；否则整体拒绝。

崩溃点与恢复语义：

- 崩在阶段一之前：无副作用、无位点变化，重新投递即可。
- 崩在两阶段之间：副作用已落盘、位点未推进；重启后 `Pending()`
  **恰好返回这些“效果已写、位点未提交”的序号**，调用方对其做幂等重放：
  以相同 action 再 `Write`（幂等），再 `Commit`，位点恰好推进一次。
- 崩在阶段二之后：位点已原子落盘，重启不会再次暴露该事件，效果不会执行两遍。

### 位点连续性规则与拒绝原因

任何失败都**整体拒绝**，副作用存储与位点保持不变，错误为可区分的 `*RejectError`：

| Kind | 触发场景 |
| --- | --- |
| `invalid_seq` | 序号 `<= 0`，或序号 `<= 已提交位点`（含重复提交已提交序号） |
| `out_of_order` | 写入序号 `> position+1`，事件越序到达 |
| `gap` | 提交序号 `!= position+1`，位点会发生跳跃 |
| `effect_missing` | 提交时该序号没有已写副作用（缺效果提交） |
| `effect_conflict` | 对同一在途序号重复写入了**不同**的副作用（不覆盖已写效果） |

重复写入**相同**副作用是幂等的，直接返回成功。

### 持久化布局（`FileStore`）

- `effects/00000000000000000001.effect`：单条副作用，
  temp 文件 fsync 后原子 rename，并 fsync 目录；
- `position.txt`：已提交位点，temp+rename 原子替换，
  读到的永远是完整的旧值或新值，不存在半个位点。

并发模型：写入与提交互斥串行判定；`Position` / `Pending` / `State`
查询与 `Verify` 自检走读锁可彼此并发；位点单调不减，每次提交至多推进一格。

### 本地验证方法

```bash
# 两阶段提交器全部用例（含竞态检测与详细判定日志）
go test -race -v ./twophase/

# 只看重放可复现性用例
go test -race -v -run TestReplayOperationSequence ./twophase/

# 只看两阶段之间崩溃恢复用例
go test -race -v -run TestCrashBetweenPhases ./twophase/
```

用**重放操作序列**核对结果（对应 `TestReplayOperationSequence`）：

1. 把一次真实消费整理成有序操作表 `[{write|commit, seq, action, 期望判定}]`，
   操作表中故意保留越序、缺效果、重复写入、位点跳跃等失败请求；
2. 在两个全新的空存储目录上分别按序执行同一份操作表；
3. 逐步比对每一步的判定原因（`ok`/`out_of_order`/`gap`/...）与位点、
   在途副作用集合，两次结果必须完全一致，最终位点相同；
4. 另可用“执行到一半删除内存态提交器、用同一目录 `New` 重开”模拟崩溃，
   断言 `Pending()` 只含已写未提交序号，重放后位点恰好推进一次。

测试日志每行都打印**操作、副作用存储（在途部分）、已提交位点与判定依据**，
可直接人工对照两阶段状态转移。
