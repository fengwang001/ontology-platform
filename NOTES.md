# 推导（K=4，序列 a a a b c d e a）与不变量

|步|值|命中|发射|步后字典(code:value)|
|1|a|否|put(0,a)|{0:a}|
|2|a|是|ref(0)|{0:a}|
|3|a|是|并入run→ref(0,2)|{0:a}|
|4|b|否|put(1,b)|{0:a,1:b}|
|5|c|否|put(2,c)|{0:a,1:b,2:c}|
|6|d|否|put(3,d)|{0:a,1:b,2:c,3:d} 满|
|7|e|否,已满|reset;put(0,e)|{0:e}|
|8|a|否(已重置)|put(1,a)|{0:e,1:a}|

最终流: put(0,a) ref(0,2) put(1,b) put(2,c) put(3,d) reset put(0,e) put(1,a)
解码: a a a b c d e a

甲: 不重置则 code=字典大小=4，误发 put(4,e)，code 越出 [0,4)。
乙: 编码端 map 残留 a:0，误发 ref(0)；解码端 {0:e}，ref(0) 解成 e（应为 a）。
丙: 排序后编码再解码得 a a a a b c d e（第4个 a 被提前，e 沉到末尾）。

不变量（保证位置 / 钉住测试）:
1 往返一致: enc.(*Coder).Append 发射、(*Coder).Decode 展开 / TestRoundTrip
2 code∈[0,K) 且 m≥1: enc.Append 分配（满先 reset）、enc.Decode 校验 / TestCodeBounds
3 RLE 无损: enc.Append 的 run 合并，put/reset 打断 run / TestRLELossless
4 失败不留痕: api.New 与 Append 前置校验、enc.Decode 纯函数局部状态 / TestRejectedLeavesState
