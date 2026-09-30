# fojoin — 全外连接增量维护器

`fojoin` 在左右两侧行集合按同一键做全外连接，只消费变更日志
（`Change{Side, Op, Key, RowID}`，插入 / 删除），增量地产出结果侧的
加减日志（`LogEntry{Add, Row}`）。下游按序应用输出日志即可得到与
批量重算一致的物化视图。

## 结果形态

对每个键：

- 左右两侧都有行：输出两侧行数乘积个**配对行** `(key, left, right)`；
- 只有一侧有行：每行输出一条**补足行**，另一侧为空位（`nil`）；
- 两侧都无：无行。

补足行与配对行是同一键上互斥的两种形态：键上两侧都非空时只存在
配对行，否则只存在补足行。空位方向固定——左行撤回（或左缺）时空位
在右侧 `(key, left, ∅)`，右行撤回（或右缺）时空位在左侧
`(key, ∅, right)`。

## 穿越零切换规则

形态切换**仅当某侧计数穿越零**（0→非零 或 非零→0）时发生：

- 本侧 0→非零 且对侧非空：先撤回对侧全部补足行，再输出本行与对侧
  的配对行；
- 本侧 非零→0 且对侧非空：先撤回本行全部配对行，再输出对侧的补足行；
- 其余情况（两侧均非零的增删、对侧为空的增删）只增删受影响的行，
  不做整键重建式输出。

切换时严格**先撤回旧形态、再输出新形态**，且被撤回的必是当前视图中
存在、值与空位形态相符的行。因此输出日志与批量重算（`Recompute` /
`SelfCheck`）始终一致。

## 非法输入

三类互不相同的可判定错误（`errors.Is` 判定）：

- `ErrEmptyKey`：键为空；
- `ErrDuplicateInsert`：重复插入同一侧同一键上已存在的行标识；
- `ErrMissingDelete`：删除不存在的行标识。

校验在状态副本上按序模拟整批变更，任一条被拒则整批不生效，状态与
已输出日志保持不变。

## 并发

`Apply` 持写锁；`View` / `Log` / `SelfCheck` 持读锁，可并发调用，
并发只读同一实例得到的视图逐字段相同。

## 本地验证

```bash
# 全部测试（日志中打印输入、结果与判定依据）
go test -v ./fojoin

# 竞态检测（覆盖并发只读一致性）
go test -race ./fojoin

# 代码检查
gofmt -l . && go vet ./...
```

测试覆盖：穿越零的补位与撤回切换（`TestZeroCrossingSwitch`、
`TestCrossingWithMultiRows`）、两侧撤回的不同处理
（`TestRetractSidesDiffer`）、非穿越不重建（`TestNonCrossingNoRebuild`）、
三类非法输入被拒后状态不变（`TestRejects`）、并发只读一致
（`TestConcurrentReadOnlyConsistent`）、日志重放与批量重算一致
（`TestLogReplayMatchesRecompute`）。
