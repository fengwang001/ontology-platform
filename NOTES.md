# 增量左外连接 NOTES

## 九步推导（视图 = 该步之后物化视图全部行）

| # | 变更 | 输出（按序） | n(x),n(y) | 视图 |
|---|---|---|---|---|
| 1 | +R r1(x) | 无 | 1,0 | （空） |
| 2 | +L l1(x) | +(l1,r1) | 1,0 | (l1,r1) |
| 3 | +L l2(y) | +(l2,NULL) | 1,0 | (l1,r1),(l2,NULL) |
| 4 | +L l3(y) | +(l3,NULL) | 1,0 | (l1,r1),(l2,NULL),(l3,NULL) |
| 5 | +R r2(y) | -(l2,NULL),+(l2,r2),-(l3,NULL),+(l3,r2) | 1,1 | (l1,r1),(l2,r2),(l3,r2) |
| 6 | +R r3(y) | +(l2,r3),+(l3,r3) | 1,2 | (l1,r1),(l2,r2),(l2,r3),(l3,r2),(l3,r3) |
| 7 | -R r2 | -(l2,r2),-(l3,r2) | 1,1 | (l1,r1),(l2,r3),(l3,r3) |
| 8 | -R r3 | -(l2,r3),+(l2,NULL),-(l3,r3),+(l3,NULL) | 1,0 | (l1,r1),(l2,NULL),(l3,NULL) |
| 9 | -R r1 | -(l1,r1),+(l1,NULL) | 0,0 | (l1,NULL),(l2,NULL),(l3,NULL) |

- (甲) 第 6 步正确输出只有 `+(l2,r3),+(l3,r3)`。若错写成「本次插入的是匹配 R 行就撤 NULL」，第 6 步会多输出 `-(l2,NULL)`、`-(l3,NULL)`；`(l2,NULL)` 在第 5 步已被撤回、计数为 0，再撤一次计数值错成 **-1**，违反不变量 2（计数只能为 0/1，`-` 必须撤回计数为 1 的行）。
- (乙) 若删除 R 行从不补回 NULL：第 9 步后视图错成**空**，比正确结果少 `(l1,NULL),(l2,NULL),(l3,NULL)` 三行。若每删一条匹配 R 行都补回：第 7 步删 r2 后 n(y)=1≠0 仍补 `+(l2,NULL)`，**第 7 步**首次违反不变量 3——`(l2,NULL)` 与 `(l2,r3)` 同时存在。
- (丙) 第 2 步会输出 `+(l1,NULL)`；视图比批量重算多 `(l1,NULL)`、少 `(l1,r1)`。

## 四条不变量的保证位置与钉住测试

1. 与批量重算一致：`api.View` 重放变更日志，`api.SelfCheck` 与 `TestViewMatchesNaiveJoin` 同朴素左外连接对拍。
2. 变更日志自洽：`ljoin.ApplyOne` 只在行存在时发 `-`；`TestLogPrefixConsistent` 逐前缀核验计数 0/1。
3. NULL 行互斥：`ljoin.ApplyOne` 插入 R 仅在插入前 n(k)=0 撤 NULL、删除 R 仅在删除后 n(k)=0 补 NULL；`TestLogPrefixConsistent`、`TestNineSteps`。
4. 失败不留痕：`api.Apply` 先 `Clone` 状态、任一条被拒即整体丢弃；四类哨兵错误互不相同；`TestRejectNoTrace`、`TestErrorsDistinct`。
