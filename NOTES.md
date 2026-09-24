# NOTES（H=3；pending 记 {delta和, 条数}）
批次 | 推送记录 | 全局视图 | pending | 热键
1 | 无 | {} | A{5,1} | ∅
2 | 无 | {} | A{8,2} | ∅
3 | A+10 | {A:10} | ∅ | {A}
4 | A−4 | {A:6} | ∅ | {A}
5 | 无 | {A:6} | B{9,2} | {A,B}
6 | A+1,B+1 | {A:7,B:1} | B{9,2} | {A,B}
7 | C+6 | {A:7,B:1,C:6} | B{9,2} | {A,B,C}
8 | C+1 | {A:7,B:1,C:7} | B{9,2} | {A,B,C}
（随后 FlushAll 按字典序推 B+9 → {A:7,B:10,C:7}）
甲：b5 后 B 不在视图（值 0/键不存在）。若批内提前查重，B+2 到达即转热并单独推送，b5 后 B 错成 2（+7 滞留缓冲）；FlushAll 后 2+7=10 仍正确。错在推送时机/批边界原子性：热键须看完整批、只对后续批次生效；该错法下不变量 2/3 仍自洽，故最终值掩盖了中间视图错误。
乙：题意的错误实现＝阈值触发后缓冲不清零而留最后一条 +2，且该键未即时转热：b4 的 −4 落入残留缓冲（+2−4=−2）不推送，b4 视图 A 错成 10（正确 6）；b6 的 +1 使其攒到 −1 触发推送、又残留 +1，FlushAll 再推 +1，A 最终错成 10（正确 7）。（若严格只改“清零”、热键语义照旧，则 −4 与 b6+1 都即时推送、b4 视图碰巧仍为 6，错误推迟到 FlushAll：残留 +2 被二次推送，A 最终错成 9；题面“b4 视图会错”对应前一种。）
丙：换序 b5={A+1,B+7}、b6={B+2,B+1}：b6 内 B 攒满 3 条立即阈值推送，b6 后 B 视图=10（上表为 1），差异源于“阈值按累计条数即时触发”叠加“热键以批次为界”；最终 FlushAll 后 B 仍为 10。不变量 1 必须带“FlushAll 后”：削峰的本质就是让 delta 合法滞留在本地缓冲，视图被允许暂时落后；若要求任意时刻相等，本地缓冲便无存在意义。
不变量（代码保证位置 / 钉住的测试函数）：
I1 FlushAll 后=批量分组求和：api.go Aggregator.FlushAll（经 local.FlushAll 字典序推尽）/ TestInvariants
I2 View+pending=已喂事件批量和：local.go Buffer.Feed 的 entry 入账与清零、glob.go View.Apply / TestInvariants
I3 推送记录可重放：glob.go View.Apply 按序追加 rec / TestInvariants
I4 失败不留痕：local.go Buffer.Feed 先整批校验后改态、api.go New 校验 H / TestRejections、TestLocalReject
复杂度：local.Buffer.probe 为非导出计数器，map 直接定位，阈值触发时置 1；仅白盒 TestProbeDirectMap 读取，无任何导出通路。
