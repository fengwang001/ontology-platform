# changelog：只追加日志的按键压缩合并器

`changelog.Log` 是一条序号连续递增（从 1 开始）的只追加写入日志，支持对任意
闭区间 `[left, right]` 按键压缩：同一键在区间内的多条写入只保留**最后一条**
（序号最大者），合并为一条压缩记录。

## API

| 方法 | 语义 |
| --- | --- |
| `New() *Log` | 空日志，`NextSeq()==1` |
| `Append(key, value) (seq, error)` | 尾部追加一条写入，返回其序号 |
| `ReadAt(key, pos) (Record, ok, error)` | 位点 `pos` 上读 `key` |
| `Compact(left, right) (CompactResult, error)` | 压缩闭区间，返回合并/删除条目数 |
| `Verify() (generation, error)` | 结构自检（只读，可并发） |
| `Records() []Record` | 当前全部记录的拷贝（批量核对用） |
| `NextSeq() int64` / `Generation() int64` | 下一序号 / 当前状态代数 |

`Record.Compacted == true` 表示该记录由区间压缩合并产生。

## 可见性规则

- **位点即序号上界**：`ReadAt(key, pos)` 只看满足 `seq <= pos` 的条目，返回其中
  序号最大者；没有任何可见条目时 `ok==false, err==nil`（不存在，不是错误）。
- **区间外条目原样保留**，序号、值、`Compacted` 标记都不变。
- **区间内按 key 合并**：每个在区间内出现过的键只保留序号最大的那条写入，
  其余删除；保留下来的记录被标记为压缩记录，**沿用其原序号**，值取最后一次写入。
- 区间内从未写入的键不产生任何压缩记录。
- 压缩记录的可见性因此从其原序号（该键在区间内最后一条写入的序号）开始。
  这保证压缩是确定性的、可复现的：对同一状态与同一区间重复压缩，日志不再变化。
- 压缩后合法位点 `1 .. NextSeq()-1` 全部仍可寻址：位点是序号上界，不依赖条目
  在物理存储上的连续排列。

## 区间边界与非法输入

设当前最新序号为 `lastSeq = NextSeq()-1`（空日志为 0）：

- 合法压缩区间：`1 <= left <= right <= lastSeq`（闭区间）。
- 非法区间按以下**可区分的哨兵错误**拒绝，校验先于任何状态变更，一次失败不会
  改变日志与位点映射：
  - `left < 1` → `ErrCompactLeftTooSmall`
  - `right > lastSeq`（空日志下任何右边界都触发）→ `ErrCompactRightTooLarge`
  - `left > right` → `ErrCompactInverted`
- 空键：`Append`/`ReadAt` 返回 `ErrEmptyKey`。
- 位点越界：`pos < 1` 或 `pos >= NextSeq()` 返回 `ErrPositionOutOfRange`。

## 并发模型

- 日志状态是不可变快照，`Append`/`Compact` 复制出新快照后用原子指针整体替换；
  变更之间用互斥锁串行化。
- `ReadAt`/`Verify` 完全无锁、彼此并发，并且可以在追加与压缩进行期间并发读取。
  每次读要么落在变更前、要么落在变更后的某个完整快照上，不会读到中间状态。
  同一实例并发读取的结果与把同一操作流串行重放的参照日志**逐值相同**
  （由 `TestConcurrentReadsDuringAppendAndCompact` 在 `-race` 下验证）。

## 本地验证方法

```bash
# 功能 + 并发测试，带竞态检测
go test -race ./...

# 查看单测审计日志：输入、读位点、返回值与判定依据逐条打印
go test -race -v ./ontology/changelog/

# 重复压测并发一致性
go test -race -count=50 -run TestConcurrent ./...

gofmt -l .
go vet ./...
```

**只看区间内条目的批量参照核对**（见 `TestCompactEveryPositionAddressable`）：

1. 独立保存压缩前的原始条目；
2. 只扫描区间 `[left,right]` 内的条目，按 key 取序号最大者并标记为压缩记录，
   区间外条目原样纳入，按序号排序得到“参照压缩日志”；
3. 断言实际压缩结果与参照**逐记录相等**；
4. 对每个合法位点 `1..NextSeq()-1` 与每个键（含从未写入的键），用同一条
   “`seq<=pos` 取序号最大”规则在参照上计算期望值，断言与实际 `ReadAt`
   逐值相同，且所有位点均不报错。
