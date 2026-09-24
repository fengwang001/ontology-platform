# 设计：基于版本向量的多副本冲突检测器

模块 `ontology`，仅用标准库，状态仅存于进程内存。包：`vv`（向量与偏序、
编解码、错误）、`store`（单副本 KV）、`sync`（副本合并、回退检出）、
`conflict`（并发兄弟、上限与丢弃）、`prune`（退役副本裁剪与安全检查）。

## 1. 版本向量与四种关系

向量 V: 副本 ID -> uint64 计数器，缺失分量视为 0。比较只遍历两向量键的
**并集 U**，对每个分量 a=V1[k]、b=V2[k]（缺失按 0）累计两个布尔：
`less` 表示存在 a<b，`greater` 表示存在 a>b。每个分量最多 2 次数值比较，
因此总比较次数 ≤ 2·|U|，与历史副本总数无关。四种关系：

- `!less && !greater`：Equal（所有并集分量相等）。
- `less && !greater`：Before（V1 严格先于 V2）。
- `!less && greater`：After（V1 严格后于 V2）。
- `less && greater`：Concurrent（既有更小分量、又有更大分量）。

关键：并发不是“不相等”。{A:1,B:0} 对 {A:0,B:1} 同时出现 less 与 greater，
判 Concurrent；{A:1,B:0} 对 {A:2,B:0} 只有 less，判 Before。缺失分量按 0，
故 {A:1} 与 {A:1,B:0} 并集上完全相等，判 Equal。

本地写入：副本 r 写入时 V[r] 加 1；V[r]==math.MaxUint64 返回 ErrOverflow，
绝不回绕。

## 2. 未知副本、回退与同步

未知副本 ID：Registry 登记所有合法 ID。比较/合并时出现未登记 ID 返回
ErrUnknownReplica（不按 0 静默处理）。

同步 merge(dst, src) 逐键合并。对每对版本按四种关系处理：After 用 src 覆盖
dst；Before 忽略；Equal 无操作；Concurrent 保留两边全部兄弟（按
(向量规范化串, 值) 去重）。并发写入的规则对称，保证收敛、幂等、交换。

计数器回退：dst 记录每个对端曾见过的最大向量 `seen`。若新收到向量在某登记
分量上严格小于 seen，返回 ErrCounterRollback，拒绝本次同步（不落地）。
合并本身做分量 max，单调不减。

## 3. 兄弟版本上限

每个键的兄弟集合大小上限 cap（构造时给定，cap≥1）。超限丢弃“最旧”：把
兄弟按 (向量字典序, 值字典序) 升序排列，丢弃排在最前的，每丢弃一个
`dropped` 计数加 1，直到 size≤cap。规则确定、与到达顺序无关。cap=1 时
退化为最后写入者胜：每次加入后只剩排序最大的一个。

## 4. 裁剪安全性

退役集合 R 后向量变为 V'（删除 R 中分量）。要求：对所有仍活跃副本，任意
两向量裁剪前后关系一致才允许裁剪。不安全的唯一来源是 Concurrent：
删除分量可能消掉 less 或 greater 的证据，使并发退化为可比/相等。
检查：对每对原本 Concurrent 的向量，裁剪后必须仍为 Concurrent
（即两侧在剩余并集上都仍各有更小与更大分量）；否则返回 ErrPruneUnsafe。
Equal/Before/After 删除公共分量后关系不变，天然安全。

## 5. 编解码与截断分类

二进制格式（大端）：magic(0xV1 1B) | #comp(4B) | 重复[idLen(2B) id nB
cnt(8B)] | keyLen(4B) key | CRC32-IEEE(4B，覆盖此前全部字节)。截断点按循环
逐字节切分并分类：字节不足以读到结构字段 → ErrHeaderIncomplete；字段读完但
某个分量/键未读全 → ErrComponentIncomplete；结构完整但 CRC 不符 →
ErrCRCMismatch。完整输入往返一致。四类故障错误均为包级哨兵，errors.Is 可辨。

## 6. 边界

零副本：Registry 可空，空向量两两 Equal；单副本：任意两向量只可能
Equal/Before/After（同一计数器链，全序）；全 0 向量两两 Equal；同副本连续
写入严格递增；cap=1 即 LWW（见第 3 节）。
