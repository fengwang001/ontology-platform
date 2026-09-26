# NOTES — 租约/心跳/栅栏（TTL=10）

七步推导（值为该步结束后状态，结果为该步返回）：
| 步 | 操作 | token | owner | expiry | 结果 |
| S1 | Acquire("L","A",0) | 1 | A | 10 | 授权，返回 1 |
| S2 | Renew("L",1,5) | 1 | A | 15 | 续期成功（5<10） |
| S3 | Renew("L",1,12) | 1 | A | 22 | 续期成功（12<15） |
| S4 | Acquire("L","B",20) | 2 | B | 30 | 授权返回 2，A 被栅栏 |
| S5 | Renew("L",1,21) | 2 | B | 30 | 拒绝 ErrStaleToken，状态不变 |
| S6 | Renew("L",2,31) | 2 | B | 30 | 拒绝 ErrExpired，状态不变 |
| S7 | Expired("L",30) | 2 | B | 30 | true（30>=30，左闭） |
(甲) 正确：ErrExpired、expiry 停在 30；若续期不看到期：expiry 错成 31+10=41，死约复活、旧主继续写。
(乙) 正确：ErrStaleToken（1≠2）；若只查 now<expiry 不查 token：A 用旧令牌把约续到 21+10=31，A、B 同时自认持锁，共 2 个持锁者。
(丙) 正确：true；若写成 now>expiry：30>30=false，边界一刻误判存活。左闭重要：到期那一刻资源必须已可交新主 Acquire，右开会让新旧主人在同一刻并存。

不变量 → 代码保证位置 → 钉住的测试：
1 与朴素重算一致：lease.go Expired 唯一判据 now>=expiry；mgr.go 未知名字直接判 true。TestExpiredMatchesNaive
2 栅栏单调：mgr.go Acquire 取旧 token+1 严格递增；lease.go Renew 先比对 token，不等即 ErrStaleToken。TestFencingMonotonic
3 续期只在存活期：lease.go Renew token 比对后再查 now>=expiry 即 ErrExpired，全部通过才写 expiry。TestRenewOnlyWhileAlive
4 失败不留痕：mgr.go 空名/未授予/token/到期四类校验全部先于 map 与堆写入；lease.Renew 先校验后赋值。TestRejectionLeavesNoTrace
另：最小堆定位见 mgr.go ExpiredAll 与非导出 scanned（TestExpiredAllScanCount）；并发见 mgr.go mu 互斥（TestConcurrentRenew）。
