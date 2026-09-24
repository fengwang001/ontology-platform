# 第三节推导：八步分步表

| 步 | 操作 | F=5 索引组 | F=8 索引组 | 本步返回 |
|---|---|---|---|---|
| 1 | Put(a,5) | [a] | [] | 成功 |
| 2 | Put(b,5) | [a b] | [] | 成功 |
| 3 | Put(c,8) | [a b] | [c] | 成功 |
| 4 | Put(a,8) | [b] | [a c] | 成功 |
| 5 | Del(b) | [] | [a c] | 成功 |
| 6 | Put(d,5) | [d] | [a c] | 成功 |
| 7 | Range(5,8) | [d] | [a c] | [d] |
| 8 | Eq(8) | [d] | [a c] | [a c] |

- (甲) 只插新组不删旧组时，第 4 步后 Eq(5) 错返回 [a b]（a 残留在 F=5 组），正确为 [b]；违反不变量 2（更新原子：主键只能出现在新 F 组）。
- (乙) 第 7 步正确返回 [d]。误作闭区间 [5,8] 会错返回 [a c d]；多出的 a、c 其 F=8=hi，左闭右开不含 hi，不该出现。
- (丙) 按规则是无操作（no-op）。若当普通插入往 F=8 组追加重复项，Eq(8) 错返回 [a a c]，正确为 [a c]。

# 四条不变量的落实位置与钉住它的测试

1. 与批量重算一致：`ient.Set.Range` 按 (F,PK) 有序收集后按 PK 排序输出（ient/ient.go）；`api.SelfCheck` 与 `TestBatchConsistent`（api/api_test.go）用模型 map 重算逐元素比对。
2. 更新原子：`tbl.put` 先 `Delete` 旧组项再 `Insert` 新组项、F 相同直接返回（tbl/tbl.go）；`api.DB` 的 `sync.RWMutex` 写锁使查询看不到中间态；`TestUpdateAtomic` 钉住。
3. 范围语义：`ient.Set.Range` 下界二分定位 (lo,"")、上界 `F<hi` 截断，结果排序去重；`TestRangeHalfOpen` 与 `TestEightSteps` 钉住。
4. 失败不留痕：`tbl.Apply` 先在副本 sim 上整体预演，任一非法即返回哨兵错误，真实记录表与索引未动（tbl/tbl.go）；`TestRejectNoTrace` 钉住。
