# NOTES：流与版本表的时态连接

## 十三步分步表（vwm 初始 -∞；`†`=墓碑；缓冲写 `eN@TS`；输出写 `eN=值`，∅ 表示 Found=false）

| 步 | vwm | k 的版本 | 拒绝 | 缓冲 | 输出 |
|---|---|---|---|---|---|
| 1 Upsert(10,A) | -∞ | 10A | - | - | 无 |
| 2 Feed(12)→e1 | -∞ | 10A | - | e1@12 | 无 |
| 3 Upsert(20,B) | -∞ | 10A 20B | - | e1@12 | 无 |
| 4 Feed(25)→e2 | -∞ | 10A 20B | - | e1@12 e2@25 | 无 |
| 5 Watermark(11) | 11 | 10A 20B | - | e1@12 e2@25 | 无 |
| 6 Upsert(12,C) | 11 | 10A 12C 20B | - | e1@12 e2@25 | 无 |
| 7 Feed(22)→e3 | 11 | 10A 12C 20B | - | e1@12 e2@25 e3@22 | 无 |
| 8 Delete(22) | 11 | 10A 12C 20B 22† | - | e1@12 e2@25 e3@22 | 无 |
| 9 Watermark(22) | 22 | 同左 | - | e2@25 | e1=C，e3=∅（22†） |
| 10 Upsert(22,X) | 22 | 同左 | 迟到版本 | e2@25 | 无 |
| 11 Feed(8)→e4 | 22 | 同左 | - | e2@25 | e4=∅（8<10 无版本） |
| 12 Upsert(30,D) | 22 | 10A 12C 20B 22† 30D | - | e2@25 | 无 |
| 13 Watermark(30) | 30 | 同左 | - | - | e2=∅（22†） |

- (甲) 第 9 步输出 e1=C(found)、e3=∅。若错取「当前最新版本」：第 9 步 e1 错成 ∅（最新是 22†；e3 碰巧仍 ∅），第 13 步 e2 错成 "D"。若 AS OF 写成 `ValidFrom<TS`：e1 错成 A、e3 错成 B。若释放条件写成 `TS<vwm`：第 9 步只输出 e1，e3 推迟到第 13 步输出。
- (乙) e2=∅、e3=∅（均落在 22† 的区间 [22,30)）。若遇墓碑跳过取更早非墓碑：e2、e3 都错成 "B"。若第 10 步迟到判定写成 `ValidFrom<vwm`：第 10 步被接受、22† 被 X 覆盖，e2 最终错成 "X"；且已输出的 e3(TS=22≤vwm=22) 结果会被该变更改写，违反不变量 3（已输出结果不可变）。
- (丙) 不缓冲、到达即连：e1=A、e2=B、e3=B、e4=∅；e1/e2/e3 与正确结果不同，e4 相同。正确实现的输出顺序为 e1(12)→e3(22)→e4(8)→e2(25)；按 TS 不单调，因为释放按水位线分批（批内按 (TS,Seq) 升序），而 e4 到达时 vwm 已越过其 TS 被立即输出，插在两批释放之间。

## 四条不变量的保证位置与钉住测试

1. 朴素一致：立即连接与释放都走同一 `tjoin.join`→`ver.AsOf`；`api.SelfCheck` 回放内置序列比对朴素扫描；测试 `TestNaiveConsistencyAfterFlush`。
2. 恰好一次且有序：事件只在到达（TS≤vwm）或 `release` 时输出一次，`release` 按 (TS,Seq) 排序（tjoin.go）；测试 `TestExactlyOnceAndOrdering`。
3. 已输出不可变：接受变更要求 `ValidFrom>vwm`（tjoin 的 ErrLate 判定），输出只追加；测试 `TestEmittedImmutable`。
4. 失败不留痕：所有校验先于任何状态变更、Seq 仅在接受时分配（tjoin 各方法开头）；测试 `TestRejectionLeavesNoTrace`。
