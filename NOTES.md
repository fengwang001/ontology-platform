# NOTES — Lamport 时钟与事件全序
九事件推导（L=执行后该节点时钟=事件时间戳；位置=正确全序位次）:
| 事件 | 节点 | L | 接收 max 操作数 | 全序位置 |
|---|---|---|---|---|
| e1 | P3 | 1 | — | 2 |
| e2 | P3 | 2 | — | 4 |
| e3 | P2 | 1 | — | 1 |
| e4 | P1 | 3 | (0, 2) | 5 |
| e5 | P1 | 4 | — | 7 |
| e6 | P2 | 2 | — | 3 |
| e7 | P2 | 5 | (2, 4) | 9 |
| e8 | P3 | 3 | (2, 2) | 6 |
| e9 | P1 | 5 | — | 8 |
正确全序（按 (时间戳,节点ID)）：e3 e1 e6 e2 e4 e8 e5 e9 e7。
(甲) 接收只做 L+1 时时间戳 e1..e9 = 1,2,1,1,2,2,3,3,3；违反时钟条件对：(e1,e4)、(e2,e4)、(e2,e5)。
(乙) 并列按执行先后破：e1 e3 e2 e6 e4 e8 e5 e7 e9；与正确全序比，位置 1、2、3、4、8、9 不同（5、6、7 相同）。
(丙) e6 与 e4 互不可达＝并发；若把全序在前当因果在前会错判 e6→e4。36 个有序对中 HB 对 21 个，并发 15 对。
不变量 → 代码保证位置 / 钉住测试：
1 与批量重算一致：hist/hist.go 的 insertLocked 用 sort.Search 按 (TS,Node) 定位、Order 直接返回该有序切片 / TestOrderMatchesBatchSort。
2 时钟条件：lc/lc.go 的 Clock.Recv 执行 max(L,t_msg)+1；hist 用 send→recv 与同节点后继边做可达性 / TestClockCondition。
3 全序严格：同节点时钟只在事件时 +1 故时间戳严格递增，键含节点 ID 故全局唯一（hist/hist.go 三个变更方法）/ TestStrictTotalOrder。
4 失败不留痕：hist/hist.go 的 Local/Send/Recv 全部在同一把互斥锁内先校验（节点/消息/重复/上限）后变更 / TestRejectedOpsLeaveNoTrace。
