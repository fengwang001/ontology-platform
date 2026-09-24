# ontology-267 NOTES

推导：L=3,H=8；ReadChunk 快照={15:c}；EndChunk=+(12,d),+(15,e)；Poll=+(10,f),-(12),+(19,g)；View={10:f,15:e,19:g}。

|LSN|条目|段|范围内|Read前|修正|Poll|输出|
|--|--|--|--|--|--|--|--|
|1|U(10,a)|≤L|是|是|否|否|无|
|2|U(15,b)|≤L|是|是|否|否|无|
|3|U(20,x)|≤L|否|是|否|否|无|
|4|U(15,c)|(L,H]|是|是|是|否|无（终值被7覆盖）|
|5|D(10)|(L,H]|是|是|是(no-op)|否|无|
|6|U(12,d)|(L,H]|是|否|是|否|End:+(12,d)|
|7|U(15,e)|(L,H]|是|否|是|否|End:+(15,e)|
|8|U(20,y)|(L,H]|否|否|否|否|无|
|9|U(10,f)|>H|是|否|否|是|+(10,f)|
|10|D(12)|>H|是|否|否|是|-(12)|
|11|U(19,g)|>H|是|否|否|是|+(19,g)|

甲：EndChunk=+(15,c)（快照直出）；Poll=+(10,f),+(19,g)（12未入视图，-(12)被吞）；View={10:f,15:c,19:g}；键15错成c（来自LSN4），正确值e丢失。
乙：Poll 比正确多 +(15,c)、+(12,d)、+(15,e)（LSN4/6/7）；键15先由e回退到旧值c(LSN4)再恢复e；最终视图仍正确，不变量1发现不了，违反不变量3。
丙：闭区间[10,20]：快照{15:c,20:x}，End=+(12,d),+(15,e),+(20,y)，最终多一行20=y；左开(10,20)：Poll=-(12),+(19,g)，最终少10=f。其后 BeginChunk(20,30) 接受（半开不相交），BeginChunk(19,25) 拒绝。

不变量1（与源表一致）：chunk.go EndChunk 把 (L,H] 折叠到快照得 H 时刻状态、Poll 续接 LSN>H；由 TestInvariantViewConsistent、TestConcurrent 钉住。
不变量2（日志自洽）：chunk 范围互不相交故完成时键必为视图新键；Poll 的 Delete 仅键存在才输出（chunk.go Poll）；由 TestInvariantOutputPrefix 钉住。
不变量3（不重复不回退）：EndChunk 只吃 (L,H] 并记录 H，Poll 严格过滤 LSN>H（chunk.go Poll）；由 TestSectionThree、TestInvariantNoRepeat 钉住。
不变量4（失败不留痕）：BeginChunk/EndChunk/Poll 全部先校验后改状态，超限在副本上模拟（chunk.go BeginChunk/EndChunk/Poll）；由 TestErrorsNoTrace 钉住。
复杂度：非导出 lastChecked 只计 Slice(L,H) 返回条目数；TestCorrectionCheckCount 对 m=100…10000 断言 ≤H−L+2。
