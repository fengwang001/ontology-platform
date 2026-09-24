# FWW 去重寄存器 — 推导与不变量

## 六步推导（单键 K；六条写入 (7,a)(3,b)(10,c)(1,d)(5,e)(8,f)）

| 步 | 写入 | 变更日志 | 生效值 | 累积丢弃 |
|---|---|---|---|---|
| 1 | (7,a) | +(K,7,a) | (7,a) | 0 |
| 2 | (3,b) | -(K,7,a) +(K,3,b) | (3,b) | 0 |
| 3 | (10,c) | 无 | (3,b) | 1 |
| 4 | (1,d) | -(K,3,b) +(K,1,d) | (1,d) | 1 |
| 5 | (5,e) | 无 | (1,d) | 2 |
| 6 | (8,f) | 无 | (1,d) | 3 |

(甲) 到达优先实现第 2 步**不会**撤回 (7,a)，最终错成 (7,a)；FWW 正确值为 (1,d)。
(乙) LWW 下第 2 步 (3,b) 落败、生效值仍 (7,a)，故第 3 步输出 -(K,7,a)、+(K,10,c)，生效值变 (10,c)，最终错成 (10,c)。FWW 保最小 Seq：更大 Seq 丢弃、更小 Seq 先撤旧值再加新值；LWW 保留/撤回谁与 FWW 逐点相反。
(丙) 另一顺序 (1,d)(7,a)(3,b)(10,c)(5,e)(8,f)：只有一条 +，撤回 0 次、丢弃 5 次（原题撤回 2 次、丢弃 3 次）；差异来自「S<当前才撤回、S>当前丢弃」。不变量 1 必须按 Seq 取最小：到达顺序可变，按到达取首个会随顺序给出不同错值。

## 不变量 → 代码保证位置 / 钉住的测试

1. 与批量重算一致：`reg.Register.Step` 仅在 Seq 更小时替换生效值（reg/reg.go），`fww.Feed` 按 Key 直接定位（fww/fww.go）— `TestRandomOrderMatchesBatch`
2. 变更日志自洽：`fww.Feed` 先发携带旧 Seq/Val 的 `-` 再发 `+`（fww/fww.go）— `TestChangelogPrefixes`
3. 生效 Seq 单调不增：只有 `reg.Register.Step` 的 `seq < cur` 分支能替换（reg/reg.go）— `TestSeqMonotonic`
4. 失败不留痕：`fww.Feed` 先整批校验、全部通过后才修改任何状态（fww/fww.go）— `TestRejectedBatchAtomic`
