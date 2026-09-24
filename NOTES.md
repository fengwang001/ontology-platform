# 时间旅行读 NOTES

## 八行分步表（写入序列：a=1@Seq1，b=10@Seq2，a=2@Seq3；第 5、6 行之间执行 Compact(2)）

| 步 | 操作 | 各 key 参与判定的版本与取舍 | 返回 |
|---|---|---|---|
| 1 | AsOf(0) | a：无 Seq≤0 的版本，不取；b：同左 | 空视图 {}（非错误） |
| 2 | AsOf(1) | a：候选{@1}，取 @1=1；b：无 Seq≤1，不取 | {a:1} |
| 3 | AsOf(2) | a：候选{@1}，取 @1=1；b：候选{@2}，2≤2 等号成立，取 @2=10 | {a:1, b:10} |
| 4 | AsOf(3) | a：候选{@1,@3}，取最新 @3=2；b：取 @2=10 | {a:2, b:10} |
| 5 | AsOf(4) | 位点 4>maxSeq=3，收敛到 3，取舍同步 4 | {a:2, b:10} |
| — | Compact(2) | a：@3>2 保留，@1 降为基线；b：@2≤2 整体降为基线（不删 key） | upto=2 |
| 6 | AsOf(2) | 2≤upto=2，不可达，不查任何版本 | ErrCompacted |
| 7 | AsOf(3) | a：取 @3=2；b：基线 @2=10 仍在链上，取 @2=10 | {a:2, b:10} |
| 8 | AsOf(4) | 收敛到 3，同步 7 | {a:2, b:10} |

**(甲)** b 可见：可见性边界是 Seq≤s，等号成立即可见，故 AsOf(2) 含 b=10。若错写成 Seq<s：AsOf(2) 会漏掉 @2，错成 {a:1}；AsOf(1) 会漏掉 @1，错成空视图 {}。
**(乙)** 仍有 b=10：b 的 @2 已降为基线版本保留在链上，AsOf(3) 取到它。若「≤upto 全丢不留基线」，AsOf(3) 错成 {a:2}（b 凭空消失，违反不变量 2）。若继续服务 AsOf(2)，会返回旧视图 {a:1,b:10}，违反不变量 3——不可达位点必须确定性报 ErrCompacted，绝不吐旧数据。
**(丙)** Compact(3)：AsOf(3) 报 ErrCompacted（3≤3）；AsOf(4) 收敛到 maxSeq=3，得 {a:2, b:10}；a 的基线是 @3=2（最新版本 ≤upto，整体降为基线，@1 丢弃）。分界必须含等号：若「s==upto 仍可达」，则违反不变量 3（s≤upto 必须确定性报 ErrCompacted），且与不变量 2 的分界矛盾——≤upto 的历史已被回收，无法重放出正确视图，只能二选一，规范选择报错。

## 四条不变量的保证位置与钉住测试

1. 与朴素重放一致：`store.Store.AsOf` 按 key 调 `hist.Chain.At` 取 Seq≤s 最新版本；测试 `TestAsOfMatchesNaiveReplay`（随机操作序列逐位点比对）。
2. Compact 不破坏可达读：`hist.Chain.Compact` 保留 ≤upto 最新版本作基线 + 全部 >upto 版本；测试 `TestCompactPreservesReachableReads`。
3. 不可达即报错：`store.Store.AsOf` 先判 `s <= compactUpto` 返回 `ErrCompacted`，不查链；测试 `TestCompactedReadErrors`。
4. 失败不留痕：三个入口先校验再动状态（`Write` 拒空 key 不分配 Seq，`AsOf`/`Compact` 拒负数）；测试 `TestRejectedOpsLeaveNoTrace`。
