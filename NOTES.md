# ontology-350 NOTES

脏集=变更基底传递闭包后代（批内取并集）；基底取本批最后一次写入值。后代：b1→{v1,v3,v4} b2→{v2,v3,v4} b3→{v4}。
1 b1=2 脏{v1,v3,v4} 末值b1=2
2 b3=7 脏{v1,v3,v4} 末值b1=2 b3=7
3 b2=4 脏{v1,v2,v3,v4} 末值b1=2 b2=4 b3=7
4 b1=5 脏{v1,v2,v3,v4} 末值b1=5 b2=4 b3=7
5 b2=1 脏{v1,v2,v3,v4} 末值b1=5 b2=1 b3=7
6 b3=2 脏{v1,v2,v3,v4} 末值b1=5 b2=1 b3=2
7 b1=3 脏{v1,v2,v3,v4} 末值b1=3 b2=1 b3=2
8 b2=6 脏{v1,v2,v3,v4} 末值b1=3 b2=6 b3=2
9 Refresh 字典序拓扑：v1,v2,v3,v4；日志 -(v1,0)+(v1,3) -(v2,0)+(v2,6) -(v3,0)+(v3,9) -(v4,0)+(v4,11)
甲：正确顺序 v1,v2,v3,v4。树展开按路径 b1→v1→v3→v4、b2→v2→v3→v4、b3→v4：v3 刷2次、v4 刷3次；v4 首次值=v3(=3，因 v2 未刷仍0，3+0)+b3(2)=5（正确11）。
乙：只刷直接依赖：v1=3 v2=6，v4 用旧 v3=0 得 0+2=2，v3 保持0；错值 v3=0、v4=2。再刷2轮收敛（轮2 v3=9，轮3 v4=11）。
丙：换序 b3=7,b1=2,b2=4,b1=5,b2=1,b3=2,b1=3,b2=6（每基底末次写在其所有写入中仍最末）：仅第1、2行脏集不同（{v4}、{v1,v3,v4}），第3-8行及第9行 Refresh 日志、最终视图全同。批内顺序无关由「脏集取并集＋基底取末值」保证；跨批顺序有关由「重算取依赖当前值、已刷视图取新值、视图值跨批保持」保证。
不变量1 与全量重算一致：rfr.go 的 Refresh 按序取当前依赖求和，api.go fullRecompute 独立复算；TestInvariantFullEquivalence 钉（随机多批序列）。
不变量2 每层恰好一次：dag.go Order 受限 Kahn 每个脏视图仅出队一次，rfr 非导出 recomputes 记录；TestRecomputeCount 白盒直读断言 m∈{100,1000,10000}。
不变量3 日志序合法：dag.go Order 入队条件＝全部脏依赖已出队；TestChangelogTopo 钉。
不变量4 失败不留痕：rfr.go AddView/SetBase 全部校验先于任何落盘（maxViews 先判、dag.AddView 原子提交）；TestRejectedLeavesNoTrace 钉。
