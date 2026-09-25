# ontology-459 推导与不变量

## 九行表（按 ≺：TS,Src,Seq 顺序消费）
1. A/0 TS5  k1 a1 —— 输出，view[k1]=a1
2. B/0 TS5  k1 b1 —— 重复丢弃
3. C/0 TS6  k4 c1 —— 输出，view[k4]=c1
4. A/1 TS7  k2 a2 —— 输出，view[k2]=a2
5. C/1 TS7  k2 c2 —— 重复丢弃
6. B/1 TS8  k3 b2 —— 输出，view[k3]=b2
7. A/2 TS9  k1 a3 —— 输出，view[k1]=a3
8. B/2 TS9  k1 b3 —— 重复丢弃
9. C/2 TS10 k5 c3 —— 输出，view[k5]=c3
终态 view={k1:a3,k2:a2,k3:b2,k4:c1,k5:c3}，Dups=3。

(甲) Src 降序：TS5 胜 B/b1，TS7 胜 C/c2，TS9 胜 B/b3；view[k1]=b3、view[k2]=c2（正解 a3、a2）。
(乙) 只按 Key 去重：k1 留首次 a1 后，a3、b3 被当重复丢弃，view[k1]=a1；本应生效的 TS9 更新 a3 被误吞（b3 本就是副本）。
(丙) 全量（含丢弃事件）LWW：k1 序列末位 ≺ 最大者是 b3（TS9 同 TS 下 B 在 A 后），view[k1]=b3；丢弃的恰是 ≺ 最小者而 LWW 取 ≺ 最大者，故不变量 1 必须限定「只取被保留事件」。

## 不变量落点
1. 与批量重算一致：merge.step 只把 winner 追加进日志，api.View 仅回放 Drain 的保留日志；钉于 TestViewMatchesBatchRecompute。
2. 变更日志自洽：merge.step 的 winner 映射保证批内 (Key,TS) 唯一，同 (Key,TS) 全体 contender 必在同一 W 批；钉于 TestChangelogNoDuplicateKeyTS。
3. 去重正确：dups 累加「批事件数−winner 数」，每 (Key,TS) 恰留一个 ≺ 最小；钉于 TestDedupWinnersAndCount。
4. 失败不留痕：merge.Add 先 msrc.Validate、查重名，全部通过才插入并入堆；钉于 TestRejectedAddLeavesNoTrace。
亚线性比较界：非导出 cmpCount（仅堆选最小 head 时自增），白盒钉于 TestMinHeadComparisonsSublinear；并发钉于 TestConcurrentViewReaders；自检钉于 TestSelfCheck。
