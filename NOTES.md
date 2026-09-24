# NOTES — MVCC 读已提交物化视图

## 第三节：15 步推导（X/Y 列 = 最新已提交版本@提交号，– = 从未提交）
| 步 | 操作 | C | X | Y | 读返回 |
|---|---|---|---|---|---|
| 1 | Begin→T1 | 0 | – | – | |
| 2 | Write(T1,X,"1") | 0 | – | – | |
| 3 | Commit(T1) | 1 | 1@1 | – | |
| 4 | Begin→T2 | 1 | 1@1 | – | |
| 5 | Write(T2,Y,"2") | 1 | 1@1 | – | |
| 6 | Commit(T2) | 2 | 1@1 | 2@2 | |
| 7 | Begin→T3 | 2 | 1@1 | 2@2 | |
| 8 | ReadTx(T3,X) | 2 | 1@1 | 2@2 | 1 |
| 9 | Begin→T4 | 2 | 1@1 | 2@2 | |
| 10 | Write(T4,X,"5") | 2 | 1@1 | 2@2 | |
| 11 | ReadTx(T3,X) | 2 | 1@1 | 2@2 | 1 |
| 12 | ReadTx(T4,X) | 2 | 1@1 | 2@2 | 5（自身待提交） |
| 13 | Commit(T4) | 3 | 5@3 | 2@2 | |
| 14 | ReadTx(T3,X) | 3 | 5@3 | 2@2 | 5 |
| 15 | ReadTx(T3,Y) | 3 | 5@3 | 2@2 | 2 |

(甲) 第14步=**5**；错成可重复读（Begin 固定快照）会得 **1**。
(乙) 第11步=**1**；错成脏读（暴露 T4 未提交写）会得 **5**。
(丙) 第12步=**5**；不显示自身未提交写会错成 **1**。

## 四条不变量的落实位置与钉住它的测试
1. 与批量参照一致：`mvcc.Commit` 把整批待提交写以同一 C 覆盖 `ver.Chain`（只留最新），`Read` 直取最新已提交；测试 `TestMatchesBatchReference`。
2. 未提交不可见：`ver.Chain` 仅在 `Commit` 时写入，`Read`/`ReadTx` 永不读他人 pending；测试 `TestNoDirtyRead`。
3. 自身写可见：`mvcc.ReadTx` 先查本事务 `ver.Pending` 再回落已提交；测试 `TestOwnWriteVisible`。
4. 失败不留痕：`mvcc.Write/Commit/ReadTx` 先完成全部校验再改任何状态，哨兵错误；测试 `TestRejectedOpsLeaveState`。
