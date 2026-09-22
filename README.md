# ontology — 两阶段提交协调者

只用标准库、状态全在进程内存的两阶段提交（2PC）协调者。不包含存储引擎、
索引、查询解析、Link、Action、HTTP。

## 包结构（依赖方向单向）

- `vote`：第一阶段投票的收集与裁决。三态（同意 / 否决 / 超时）、裁决规则，
  不依赖其他包。
- `participant`：参与者本地状态机（待定 → 已预备 → 已提交/已中止）与幂等
  指令处理，依赖 `vote`。
- `coord`：协调者。注入时钟、驱动两阶段、记录决议，依赖 `participant` 与
  `vote`。

## 关键语义

- **全票才提交**：任一否决或超时 → 全体中止，已同意者也会收到中止并回滚。
- **超时等价于否决但可区分**：决议原因区分 `rejected` 与 `timeout`，并指出
  是哪个参与者（`Decision.Culprit`）。截止判定按注入时钟、左闭右开：
  `now == deadline` 即算超时。
- **决议一次写定**：写下后不可更改；迟到票与重复驱动都返回第一次的结果。
- **指令幂等**：重复的提交/中止指令不改变状态，`CommitCount()` 只增一次。
- **非法转移可判定**：`ErrNotPrepared`、`ErrCommitAfterAbort`、
  `ErrAbortAfterCommit` 三个哨兵错误彼此可区分，且不改变状态。
- **空参与者集合：提交（commit）**。理由：「全体同意」在空集上是空真
  （vacuous truth），而中止只应由否决或超时触发；空集两者皆无，故提交。
  该行为在 `coord` 包文档注释中同样写明，且由测试锁定。
- **恢复重放**：`Txn.Replay()` 把已写下的决议重新投递给所有参与者；因指令
  幂等，最终状态与提交计数不变。
- **时间只走注入时钟**：构造时传入 `now func() time.Time`，代码不调用
  `time.Now` / `time.After` / `time.AfterFunc`。

## 运行

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go run ./cmd/demo
```
