# NOTES — 迟到事件侧输出分流

## 八事件分步表（maxSide 足够大；wm 未定义记「无」）

| # | 事件 | 到达时 wm[key] | 判定 | gap | 步后 wm | 步后 view |
|---|---|---|---|---|---|---|
| 1 | (A,10) | 无 | 主路（首见） | 无 | A=10 | {A:10} |
| 2 | (B,20) | 无 | 主路（首见） | 无 | B=20 | {A:10,B:20} |
| 3 | (A,15) | A=10 | 主路（15>10） | 无 | A=15 | {A:15,B:20} |
| 4 | (A,15) | A=15 | 主路（15==15） | 无 | A=15 | {A:15,B:20} |
| 5 | (B,20) | B=20 | 主路（20==20） | 无 | B=20 | {A:15,B:20} |
| 6 | (B,25) | B=20 | 主路（25>20） | 无 | B=25 | {A:15,B:25} |
| 7 | (A,12) | A=15 | 侧路（12<15） | 3 | A=15 | {A:15,B:25} |
| 8 | (A,10) | A=15 | 侧路（10<15） | 5 | A=15 | {A:15,B:25} |

- (甲) 两步 TS 都**等于**水位，正确均判**主路**（==算主路）。若写成「TS>wm 才是主路」：第 4 步 (A,15)、第 5 步 (B,20) 被错分到**侧路**，gap 错成 **0**（侧路出现 gap=0，违反 gap>0），主路少 2 条、侧路多 2 条。
- (乙) 全局水位在第 2 步后 = **20**；第 3 步 (A,15)<20 被错分到**侧路**（gap 错成 5），第 4 步 (A,15) 同样错判侧路。最终主路集合少这两条 (A,15)，只剩 (A,10)(B,20)(B,20)(B,25) 共 4 条。
- (丙) Feed 无条件 `view[key]=ts`：第 7 步后 view[A] 错成 **12**，第 8 步后错成 **10**；正确 view[A] 恒为 **15**（wm 只由主路推进）。

## 四条不变量的保证位置与钉住测试

1. 与批量重算一致 — `route/route.go` Router.Feed：视图只取自主路 Advance 后的每 Key wm，View 读 wm；`api/api_test.go` TestBatchConsistency（循环多档随机顺序）钉住。
2. 分流自洽 — `wm/wm.go` Watermark.Classify：首见或 ts>=v 判主路（含相等），迟到返回 gap=v-ts>0；TestEightStepSequence（api）与 TestClassifyTable（wm）钉住。
3. 水位单调 — `wm/wm.go` Watermark.Advance：仅未定义或 ts>v 才写，侧路事件不调用它；`wm/wm_test.go` TestWatermarkMonotonic 钉住。
4. 失败不留痕 — `route/route.go` Router.Feed：空 Key/负 TS 在触碰状态前返回，侧路上溢在追加侧路前返回（首见事件必为主路，故上溢时不写 map）；`api/api_test.go` TestRejectionNoTrace 钉住。
