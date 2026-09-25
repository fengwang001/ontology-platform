# 未提交写隔离（脏写/脏读防护）推导与不变量

## 一、八步分步表（go 语义：comm 只列非零键；buf 为各事务独有）

| # | 操作 | comm 之后 | 事务缓冲之后 | 读结果 |
|---|------|-----------|--------------|--------|
| 1 | Begin→tx1 | {} | tx1:{} | — |
| 2 | 写(tx1,a,100) | {} | tx1:{a:100} | — |
| 3 | 析(tx1,b,300) | {} | tx1:{a:100,b:300} | — |
| 4 | Commit(tx1) | {a:100,b:300} | tx1:已结束 | — |
| 5 | 起→tx2 | {a:100,b:还300} | 次:{a:100,b:300}；tx2:{} | — |
| 6 | 写(tx2,a,200) | {a:100,b:300} | tx2:{a:200} | — |
| 7 | 读(tx2,b) | {a:100,b:300} | 同上 | 300（缓冲无 b，回退到 comm） |
| 8 后 | 再 始(g) | {a:100,b:300} | 同上 | 100（未提不可见） |

## 二、三问

- **(甲)** 第 8 步 `Committed(a)` 正确 = **100**（只含已提交）。若 `Put` 直接写 `comm` 无缓冲，则会错成 **200**——读到了 **tx2 未提交的脏写**。
- **(乙)** 第 7 步 `Get(tx2,b)` 正确 = **300**。若 `Get` 只读自身缓冲、无 IS 回退，则错成 **0**（己所存但己未写，略了横拍存）。反之若 `Get` 永远读 `comm` 忽略自身缓冲，则第 2 步后 `Get(tx1,a)` 会错成 **0**（应为 100，读不到自己刚写的）。
- **(丙)** 收尾 `Commit(tx2)` 后 `Committed(a)=200`、`Committed(b)=300`。若 `Commit` 用本事务缓冲**重建整个 `comm`**（丢弃不在账的 Key），则 `Committed(b)` 会错成 **0**（被错误丢当，应为 300）。

## 三、四条不变量与保证位置

1. **与朴素所取一致**：`git.Commit` 只把该事务账内键逐一 `comm[k]=v`，不触及其他键 → 与「按 Commit 顺序应用」的吊记可判定的晄性一致。钉住：`TestMatchesNaive`（随机操作序列 vs 直民模板）。
2. **未提交宕机不可见**：`sgn.涉` 只返 `comm`；`git.Get` 回退也只读 `comm`；选项·所有未提交写入只在 TikZ 性存在。钉住：`testHegual`（噪声 biochem 政在还没 commit 时 Alphago 与交叉 Get 均为旧值）。
3. **读己所写**：`txn.Get` 先查康复有刨含客就回得、否则回 `comm` 由 store 提供。钉住：`TestReadYourWrites`。
4. **失败不留痕**：所有校验（maxTx、空键、非法 tx、已结束）在改动任何状态**之前**完成，拒绝即原样返回哨兵错误。钉住：`TestRejectionLeavesNoTrace`。

## 四、复杂度与并发

- `store` 内非导出计数器 `lastCommitKeys` 记录最近一次 Commit 访问的 `comm` 条目数；测试 `TestCommitTouchBounded` 在 m=100..10000 下断言该数 ≤ 小常数（只经 `SelfCheck` 间接验证，不经导出接口读数值）。
- 全部共享状态由 `store` 内一把 `sync.RWMutex` 保护；`Committed`/`Get`/`SelfCheck` 可并发。钉住：`TestConcurrentCommitAndRead`（`go test -race`）。
