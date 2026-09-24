# NOTES

## 第三节推导

事务划分：T1=pid1 首0 {0:a,2:c} Commit@3；T2=pid2 首1 {1:b,5:e} Abort@6；T3=pid3 首4 {4:d,7:f} Commit@9；T4=pid1 首8 {8:g} Abort@10。未决=首位点<HW 且（无控制标记或标记位点>=HW）；LSO=min(HW, 未决首位点)。

| 步 | HW | 未决事务(pid,首) | LSO | Fetch 区间 | 输出 |
|---|---|---|---|---|---|
| 1 | 2 | (1,0),(2,1) | 0 | [0,0) | 无 |
| 2 | 4 | (2,1) | 1 | [0,1) | a |
| 3 | 6 | (2,1)（Abort@6 不<6） | 1 | [1,1) | 无 |
| 4 | 7 | (3,4) | 4 | [1,4) | c |
| 5 | 9 | (3,4)（Commit@9 不<9） | 4 | [4,4) | 无 |
| 6 | 10 | (1,8) | 8 | [4,8) | d,f |
| 7 | 11 | 无 | 11 | [8,11) | 无 |

(甲) 正确：第1步「无」、第2步「a」。若用 HW 代替 LSO（只滤此刻已知中止）：第1步 [0,2) 输出 a,b；第2步 [2,4) 输出 c。其中 b=Data(2,b) 之后被 Abort@6 证明属于中止事务 T2——它被错误地先吐给了消费者。
(乙) 不过滤中止事务（仍以 LSO 为上界、跳过控制标记）：七步拼接为 a,b,c,d,e,f,g（7 条）；正确序列是 a,c,d,f（4 条）。若同时还把控制标记当记录输出，再多 4 条（位点 3,6,9,10 的 Commit(1)、Abort(2)、Commit(3)、Abort(1)）。
(丙) 按生产者判定中止：HW=11 时 pid1 末标记 Abort@10 → 滤掉其全部数据 a,c,g；pid2 末标记 Abort@6 → 滤掉 b,e；pid3 末标记 Commit@9 → 留 d,f。消费者读到 d,f；正确应读 a,c,d,f。差异来自「事务=该 pid 上一控制标记之后的数据记录、结局按事务判定」这条规则：同一 pid 可先后有提交与中止的多个事务，Abort 只抹掉它所属的那一个事务，不株连该 pid 已提交的旧事务（a,c 属于已提交的 T1，不应被 T4 的 Abort 牵连）。

## 四条不变量

1. 与批量参照一致：rc.Fetch 严格按 [from,LSO) 扫描、过滤后 next=LSO（rc/rc.go 的 Fetch）；钉于 TestFetchMatchesBatchReference。
2. LSO 合法且单调：txlog.AdvanceHW 用按首位点有序的未决队列头计算 lso=min(h, 队首首位点)（txlog/txlog.go 的 AdvanceHW）；钉于 TestLSOLegalMonotone。
3. 不暴露未决与中止：rc.Fetch 只放行 Kind==Data 且 Committed 的记录；txlog.Scan 仅当控制标记位点<HW 且结局为 Commit 才置 Committed；钉于 TestNoUndecidedOrAbortedExposed。
4. 失败不留痕：AppendData/AppendCommit/AppendAbort/AdvanceHW/Fetch 全部先校验后变更，非法即返回哨兵错误（txlog/txlog.go、rc/rc.go 各校验处）；钉于 TestRejectedOpsLeaveStateUnchanged。
