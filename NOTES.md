# NOTES：读已提交物化视图推导与不变量落点（值@提交号，—=不存在）

| 步 | 操作 | C | X 最新已提交 | Y 最新已提交 | 读返回 |
|----|------|---|---|---|---|
| 1 | Begin→T1 | 0 | — | — | |
| 2 | Write(T1,"X","1") | 0 | — | — | |
| 3 | Commit(T1) | 1 | 1@1 | — | |
| 4 | Begin→T2 | 1 | 1@1 | — | |
| 5 | Write(T2,"Y","2") | 1 | 1@1 | — | |
| 6 | Commit(T2) | 2 | 1@1 | 2@2 | |
| 7 | Begin→T3 | 2 | 1@1 | 2@2 | |
| 8 | ReadTx(T3,"X") | 2 | 1@1 | 2@2 | "1" |
| 9 | Begin→T4 | 2 | 1@1 | 2@2 | |
| 10 | Write(T4,"X","5") | 2 | 1@1 | 2@2 | |
| 11 | ReadTx(T3,"X") | 2 | 1@1 | 2@2 | "1" |
| 12 | ReadTx(T4,"X") | 2 | 1@1 | 2@2 | "5" |
| 13 | Commit(T4) | 3 | 5@3 | 2@2 | |
| 14 | ReadTx(T3,"X") | 3 | 5@3 | 2@2 | "5" |
| 15 | ReadTx(T3,"Y") | 3 | 5@3 | 2@2 | "2" |

- (甲) 第 14 步="5"；若错成可重复读（Begin 即固定快照），T3 始终读旧快照得 "1"。
- (乙) 第 11 步="1"；若错成脏读（暴露他人未提交写），会读到 T4 的 "5"。
- (丙) 第 12 步="5"；若不把自身未提交写给自己看，T4 只见已提交的 "1"。

不变量 → 代码保证位置 / 钉住的测试函数：
1. 与批量参照一致：`mvcc.(*Store).Commit` 持锁令 C++ 并原子应用 pending、`ver.Entry.Apply` 只留最大提交号版本；`api.TestBatchReferenceEquivalence`。
2. 未提交不可见：待提交只存于 `txState.pending`，`Read/ReadTx` 只查已提交 `keys`；`api.TestUncommittedInvisible`。
3. 自身写可见：`mvcc.(*Store).ReadTx` 先查自身 pending 再查已提交；`api.TestOwnWriteVisible`。
4. 失败不留痕：四类校验均在任何状态修改前返回哨兵错误（含计数器不动）；`api.TestRejectedOpsLeaveNoTrace`，错误互异由 `mvcc.TestFaultSentinels` 钉住。
