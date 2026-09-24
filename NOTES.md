# NOTES

## 八步推导（P=prepared F=finalized D=discarded，暂存删除记 ×，S=暂存区 V=视图 L=日志）
1. Put(k1,a)    S={k1:a}              V={}                      L=-
2. Put(k2,b)    S={k1:a,k2:b}         V={}                      L=-
3. Commit       S={}                  V={k1:a,k2:b}             L=1F
4. Put(k1,c)    S={k1:c}              V={k1:a,k2:b}             L=1F
5. Del(k2)      S={k1:c,k2:×}         V={k1:a,k2:b}             L=1F
6. CommitCrash  S={}                  V={k1:a,k2:b}             L=1F 2P
7. Put(k3,d)    S={k3:d}              V={k1:a,k2:b}             L=1F 2P
8. Commit       S={}                  V={k1:a,k2:b,k3:d}        L=1F 2P 3F
9. Recover()→2  S={}                  V={k1:a,k2:b,k3:d}        L=1F 2D 3F

（甲）第6步后视图仍是 {k1:a,k2:b}；错把「日志有条目=已生效」者此刻会看到 {k1:c}（k2 已删）。Recover 丢弃的是提交 2。
（乙）不区分 P/F、把提交2当生效，最终错成 {k1:c,k3:d}（缺 k2、k1 值错）；正确最终视图 {k1:a,k2:b,k3:d}。
（丙）全量重放时提交2令 k1=c 且删除 k2，提交3再加 k3=d，得 {k1:c,k3:d}——k1 错值、k2 丢失；只按 id 升序重放 F 才正确。第二次 Recover 返回 0，2 保持 D、无任何状态变化（幂等）。

## 四条不变量：保证位置 / 钉住测试
1. 与朴素重放一致：视图仅由 clog.Finalize 的应用循环写入（clog/log.go），AppendPrepared 不触视图、Recover 只置 D，api.View 只返回视图副本；TestNaiveReplayEquivalence。
2. 未提交不可见：api.Put/Del 校验后只写 stg 暂存区（api/api.go），View 不读暂存；TestStagedInvisible。
3. 半成品零效果：CommitCrash 只调 AppendPrepared（clog/log.go），视图永不反映 P，Recover 把最新 P 置 D；TestPreparedZeroEffect。
4. 失败不留痕：api.Put/Del/Commit 的哨兵错误校验全部先于任何状态变更（api/api.go）；TestRejectionLeavesNoTrace。
