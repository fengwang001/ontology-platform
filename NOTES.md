# NOTES — 状态机复制推导

## 五步表（committed / lastApplied / State）

| 步骤 | committed | lastApplied | State |
|---|---|---|---|
| S1 Append 四条 | 0 | 0 | 0 |
| S2 Commit(3) | 3 | 0 | 0 |
| S3 Apply() | 3 | 3 | 7 |
| S4 Restart(2,6)；Commit(4) | 4 | 2 | 6 |
| S5 Apply() | 4 | 4 | 12 |

- **甲**：S3 正确 State=7（(0+2)*3+1）。倒序应用：0+1=1、×3=3、+2=**5**（应 7）。体现 Add/Mul **不可交换、对顺序敏感**。
- **乙**：终止条件错成日志末尾会多应用第 4 条 Add(5)：7+5=**12**（应 7），错在**未提交的下标 4**。
- **丙**：Restart(2,6) 后 lastApplied=**2**；Apply 只补第 3、4 条，State=6+1+5=**12**。若 lastApplied 被重置为 0，会在快照态 6 上重放 1..4：8→24→25→**30**（应 12）。

## 不变量：保证位置 / 钉住测试

1. **重算一致**：`repl.go` 的 Apply 从 lastApplied+1 续算、Restart 直接装入快照态；测试 `TestInvariantRecompute`。
2. **索引自洽**：`repl.go` 的 Commit/Restart 先边界校验后改值、Apply 逐条 lastApplied++；测试 `TestIndexInvariant`。
3. **确定收敛**：`sm.go` 的 Apply 为纯函数、`repl.go` 按下标升序应用；测试 `TestBatchingConverges`。
4. **失败不留痕**：`repl.go` 的 Append/Commit/Restart 校验全部先于状态变更，错误为互不相同哨兵；测试 `TestRejectionLeavesNoTrace`。
