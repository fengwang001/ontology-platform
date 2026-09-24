# CDC applier NOTES
记法:Ap=已应用 DL=死信;阻塞列 `X#n/t`=队首 X#n 已尝试 t 次;缓冲列为各已见 Key 的 FIFO。
|步|本步定案记录|阻塞队首/次数|缓冲队列|死信|
|---|---|---|---|---|
|1|Ap A#1|无|A:[]|[]|
|2|无|A#2/1|A:[]|[]|
|3|无|A#2/1,B#1/1|A:[],B:[]|[]|
|4|无|A#2/1,B#1/1|A:[A#3],B:[]|[]|
|5|Ap C#1|A#2/1,B#1/1|A:[A#3],B:[],C:[]|[]|
|6|无|A#2/1,B#1/1|A:[A#3],B:[B#2]|[]|
|7|Ap A#2,Ap A#3|B#1/2|A:[],B:[B#2]|[]|
|8|无|B#1/2|A:[],B:[B#2,B#3]|[]|
|9|Ap A#4|B#1/2|B:[B#2,B#3]|[]|
|10|DL B#1,Ap B#2|B#3/1|B:[]|[B#1]|
|11|Ap B#3|无|全空|[B#1]|
甲(全局阻塞):C#1 进缓冲不尝试;第5步末已应用仅 1 条 A#1(正确 2 条:A#1,C#1);违反不变量2。
乙(失败不阻塞):A 次序 A#1,A#3,A#2,A#4(错);正确 A#1,A#2,A#3,A#4。
丙(LIFO 排空):第10步仅 DL B#1;其后队首 B#3/1、缓冲 [B#2];B 最终 DL B#1,Ap B#3,Ap B#2(错);正确 DL B#1,Ap B#2,Ap B#3。
## 不变量:代码保证位置 / 钉住测试
1 同键保序:kq.go 队首不占缓冲+FIFO、applier.go 排空顺序与 lastSeq 准入;TestOrderedTrace、TestSerialReferenceRandom。
2 故障隔离:applier.go Submit 中 Key 未阻塞即做首次尝试;TestIsolation(与 TestOrderedTrace 第5步)。
3 串行参照一致:applier.go Tick 开始时快照阻塞集、按 Key 字典序、FIFO 排空;TestSerialReferenceRandom、TestConcurrentPerKey。
4 失败不留痕:api.go New 参数校验、applier.go Submit 全部校验先于任何改态;TestRejectedOpsLeaveNoTrace。
