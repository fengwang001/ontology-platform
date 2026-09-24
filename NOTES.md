# NOTES

## 七批分步推导（U=Upsert，D=Delete；—=不存在，表记 {k:v}）
1. B1 触及 a,b | a:—→2, b:—→1 | +(a,2) +(b,1) | 表 {a:2,b:1}
2. B2 触及 a | a:2→a:2（值相等）| 无 | {a:2,b:1}
3. B3 触及 c | c:—→c:—（删不存在→U→再删）| 无 | {a:2,b:1}
4. B4 触及 b | b:1→b:1（值相等）| 无 | {a:2,b:1}
5. B5 触及 b,a（首次序）| b:1→3, a:2→— | -(b,1) +(b,3) -(a,2) | {b:3}
6. B6 触及 a | a:—→7 | +(a,7) | {a:7,b:3}
7. B7 触及 d,b | d:—→—（U后即删）, b:3→— | -(b,3) | {a:7}
折叠总条数 = 2+0+0+0+3+1+1 = 7。

(甲) 逐条语义：B1 变成 +(a,1) +(b,1) -(a,1) +(a,2)（4条）；B2 变成 -(a,2) +(a,2)（2条）；B3 变成 +(c,5) -(c,5)（2条，开头 D(c) 无输出）；B7 变成 +(d,1) -(d,1) -(b,3)（3条）。七批总条数 7 → 15。
(乙) 只有 B6 不同：a 在 B5 被删后留下保留旧值 2 的墓碑，B6 的 before 被误判成"有旧值"，错输出 -(a,2) +(a,7)（正确只有 +(a,7)）。-(a,2) 违反不变量2（撤回时 a 根本不存在，更不等于 2），同时违反不变量3（B6 前后差异仅 1 条却输出 2 条）；不变量1仍成立（净值表不变）。
(丙) B2 错成 -(a,2) +(a,2)、B4 错成 -(b,1) +(b,1)，其余批不变；总条数 7 → 11。不变量1、2、4仍成立（每对 -旧/+旧 在其前缀处都合法、净值为零，失败语义未触及），被违反的是不变量3（零差异批输出 2 条，非极小）。

## 四条不变量：保证位置 / 钉住的测试
- I1 与朴素参照一致：norm.Apply 只按 touched 首次序生成差异并整批 append 进 log，Snapshot 即 cur 的拷贝 —— TestReferenceEquivalence
- I2 日志每个前缀自洽：rfold.Diff 仅在 beforeOK 时发 -、仅在 afterOK 时发 + 且先 - 后 +；norm 以批前 before 映像为准 —— TestLogPrefixesValid
- I3 极小：rfold.Diff 首行对"都不存在/都存在且等值"短路返回 nil；norm 只遍历 touched、checked 只计 touched —— TestMinimality、TestComplexityCounter
- I4 失败不留痕：norm.Apply 先整批 Validate 再模拟（非法操作绝不触表）；超限按 before 映像逐 Key 回滚，log 不动 —— TestRejectionAtomic
