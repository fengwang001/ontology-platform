# 多 CDC 源有序归并与跨源去重 — 推导与不变量

## 九行分步表（≺：TS 升序→Src 字典序→Seq 升序；批 = head.TS==W 的源各弹一个）

| # | 事件 (Src,Seq,TS,Key,Val) | 判定 | 输出后 view 更新 |
|---|---|---|---|
| 1 | A,0,TS=5,k1,a1 | 输出（W=5，批{A,B}，(k1,5) 取 (A,0)） | k1=a1 |
| 2 | B,0,TS=5,k1,b1 | 重复丢弃（dup#1） | — |
| 3 | C,0,TS=6,k4,c1 | 输出（W=6，批{C}） | k4=c1 |
| 4 | A,1,TS=7,k2,a2 | 输出（W=7，批{A,C}，(k2,7) 取 (A,1)） | k2=a2 |
| 5 | C,1,TS=7,k2,c2 | 重复丢弃（dup#2） | — |
| 6 | B,1,TS=8,k3,b2 | 输出（W=8，批{B}） | k3=b2 |
| 7 | A,2,TS=9,k1,a3 | 输出（W=9，批{A,B}，(k1,9) 取 (A,2)） | k1=a3 |
| 8 | B,2,TS=9,k1,b3 | 重复丢弃（dup#3） | — |
| 9 | C,2,TS=10,k5,c3 | 输出（W=10，批{C}） | k5=c3 |

最终 view：k1=a3, k2=a2, k3=b2, k4=c1, k5=c3；Dups=3。

- (甲) 并列比较错写成 Src 降序（C>B>A）：TS=5/k1 胜者 B(b1)，TS=7/k2 胜者 C(c2)，TS=9/k1 胜者 B(b3)；view[k1] 错成 b3，view[k2] 错成 c2。
- (乙) 去重只看 Key 忽略 TS：k1 在 TS=5 已见 → TS=9 的 a3 被误吞，view[k1] 错成 a1；被误吞的本应生效更新是 A 的 (TS=9,k1,a3)。
- (丙) 批量重算若含被丢弃事件：k1 按 ≺ 依次被 a1,b1,a3,b3 覆盖 → 错成 b3。被丢弃副本会在重算里覆盖胜者，故不变量 1 必须写成「只取被保留的事件」。

## 四条不变量 → 代码位置 → 钉住它的测试

1. 与批量重算一致：`api` 在 `drain` 里按日志顺序 LWW 应用（api.go）；测试 `TestViewMatchesBatchRecompute`（api_test.go，多档规模 × 随机注册顺序）。
2. 变更日志自洽：`merge.Step` 批内同 (Key,TS) 只留一个、输出按 ≺ 升序（merge.go）；测试 `TestLogSelfConsistent`。
3. 去重正确：`merge.Step` 每组保留 ≺ 最小、其余计入 dups（merge.go）；测试 `TestDedupExact`（dups == 输入总数 − 日志长度，且每个 (Key,TS) 恰留一个）。
4. 失败不留痕：`merge.AddSource` 先完成全部校验再改任何状态（merge.go）；测试 `TestRejectedOpsLeaveNoTrace`。
