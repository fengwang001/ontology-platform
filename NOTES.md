# NOTES

## 第三节推导（列 a,b,c；批前行 k：a="1", b="x", c=NULL）

| # | 事件 Set/Before | 合并后 Set | 合并后 Before | 此刻行 k |
|---|---|---|---|---|
| 1 | {a:"2"}/{a:"1"} | {a:"2"} | {a:"1"} | a="2", b="x", c=NULL |
| 2 | {b:NULL}/{b:"x"} | {a:"2",b:NULL} | {a:"1",b:"x"} | a="2", b=NULL, c=NULL |
| 3 | {a:"3",c:"z"}/{a:"2",c:NULL} | {a:"3",b:NULL,c:"z"} | {a:"1",b:"x",c:NULL} | a="3", b=NULL, c="z" |
| 4 | {b:"x"}/{b:NULL} | {a:"3",c:"z"}（b 等值剔除） | {a:"1",c:NULL} | a="3", b="x", c="z" |
| 5 | {c:NULL}/{c:"z"} | {a:"3"}（c 等值剔除） | {a:"1"} | a="3", b="x", c=NULL |
| 6 | {a:NULL}/{a:"3"} | {a:NULL} | {a:"1"} | a=NULL, b="x", c=NULL |

最终输出：Update k，Set={a:NULL}，Before={a:"1"}；与朴素逐条应用结果一致。

- (甲) 缺席列当 NULL 应用：第 1 步后错误行 a="2",b=NULL,c=NULL（正确 a="2",b="x",c=NULL）；第 6 步后错误行 a=NULL,b=NULL,c=NULL（正确 a=NULL,b="x",c=NULL）。
- (乙) 同列保留先到值：第 6 步合并 Set={a:"2",b:NULL,c:"z"}、Before={a:"1",b:"x",c:NULL}，下游行 a="2",b=NULL,c="z"，与正确行 a=NULL,b="x",c=NULL 完全不同。
- (丙) Before 取最后一次：第 6 步合并 Set={a:NULL,b:"x",c:NULL}、Before={a:"3",b:NULL,c:"z"}；下游应用后的行与正确结果相同，但 Before 不镜像批前值，违反不变量 2。

## 四条不变量

1. 与朴素参照一致：cbatch.ApplyBatch 先合并再整体应用，合并语义在 pcol.Merger.Merge；测试 TestRandomBatchNaiveConsistency。
2. before 镜像：pcol.Merger.Merge 只保留首触 Before 并剔除等值列，cbatch.ApplyBatch 校验 Before 等于当前实际值；测试 TestBeforeMirror。
3. 至多一条+首现顺序：cbatch.ApplyBatch 用 accs 归并、order 记录首现顺序；测试 TestOutputOrderUnique。
4. 失败不留痕：cbatch.ApplyBatch 全部事件校验通过后才写表；测试 TestFailureAtomic。
