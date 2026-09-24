# GROUPING SETS 增量聚合：推导

ID 编码（与题给数值一致）：ID = GROUPING(A)*4 + GROUPING(B)*2 + GROUPING(C)*1，故 (A)=3、(A,B)=1、()=7。
G = {(A),(A,B),()}。每行：本步后 () 总sum / (A) 条目 / (A,B) 条目（xp=(x,p)，xq=(x,q)，yp=(y,p)）

| 步 | 事实 | () | (A) | (A,B) |
| 1 | +f1 x p u 10 | 10 | x=10 | xp=10 |
| 2 | +f2 x p v 20 | 30 | x=30 | xp=30 |
| 3 | +f3 x q u 5 | 35 | x=35 | xp=30,xq=5 |
| 4 | +f4 y p u 7 | 42 | x=35,y=7 | xp=30,xq=5,yp=7 |
| 5 | −f2 x p v 20 | 22 | x=15,y=7 | xp=10,xq=5,yp=7 |
| 6 | +f5 y p u 3 | 25 | x=15,y=10 | xp=10,xq=5,yp=10 |
| 7 | −f1 x p u 10 | 15 | x=5,y=10 | xp 归零移除；xq=5,yp=10 |
| 8 | +f6 x q u 2 | 17 | x=7,y=10 | xq=7,yp=10 |

(甲) (A,B) ID=1，() ID=7；(A,B) 中 GROUPING(C)=1。极性写反等于掩码逐位取反：(A,B) 错成 1^7=6，() 错成 7^7=0。
(乙) ROLLUP(A,B,C) 额外多出 (A,B,C)：ID=0，GROUPING=(0,0,0)。CUBE 额外多 5 个：(B)=5、(C)=6、(A,C)=2、(B,C)=4、(A,B,C)=0。
(丙) 第 7 步后 (x,p)=0，必须移除。若零值也物化，第 8 步后 (A,B) 多出 (x,p)=0，live 条目数 2 错成 3。

## 四条不变量：保证位置 / 钉住测试

1. 与批量重算一致：`agg.(*Table).Apply` 逐组按投影键增量累加、归零即 delete（agg/agg.go）→ TestBatchEquivalence
2. 组完整性：`api.New` 只把指定子集交给 agg，`View` 键取 `gset.Group.ID()`（api/api.go、gset/gset.go）→ TestGroupCompleteness
3. 撤回不越界：`agg.Apply` 先对所有组预检 newSum>=0 再提交（agg/agg.go）→ TestWithdrawalRejected
4. 失败不留痕：api 前置校验失败直接返回，agg 预检在任何写入前完成（api/api.go、agg/agg.go）→ TestRejectAtomic
复杂度计数器 probe 为非导出字段：TestProbeCountConstant（包 agg 内测试）。并发只读：TestConcurrentReaders。
