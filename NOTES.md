# K=4 推导：追加 a a a b c d e a

| 步 | 值 | 命中 | 发射 token | 步后字典 |
|---|---|---|---|---|
| 1 | a | 否 | put(0,a) | {0:a} |
| 2 | a | 是 | ref(0) | {0:a} |
| 3 | a | 是 | ref(0,2)（合并第2、3次） | {0:a} |
| 4 | b | 否 | put(1,b) | {0:a,1:b} |
| 5 | c | 否 | put(2,c) | {0:a,1:b,2:c} |
| 6 | d | 否 | put(3,d) | {0:a,1:b,2:c,3:d}（满） |
| 7 | e | 否（满） | reset，put(0,e) | {0:e} |
| 8 | a | 否（重置后） | put(1,a) | {0:e,1:a} |

最终流：put(0,a) ref(0,2) put(1,b) put(2,c) put(3,d) reset put(0,e) put(1,a)
解码：a a a b c d e a（与原序逐元素相同）
(甲) 不重置则 code=len=4，误发 put(4,"e")，code=4 越界 [0,4)，解码端拒绝。
(乙) 编码端 map 残留 a:0 → 误发 ref(0)；解码端重置后 dict[0]="e"，解成 "e" 而非 "a"。
(丙) 排序后编码 a a a a b c d e，解码错成 a a a a b c d e（最后的 a 被提到最前）。

## 不变量（保证位置 / 钉住测试）
1 往返一致：enc.go `Append`/`emitRef` 与 `Decode` 对称；TestRoundTrip
2 code∈[0,K)、m≥1：dict.go `Add`（未满才取 len）、enc.go `Append` 满先 reset、`Decode` 校验；TestCodeBounds
3 RLE 无损：enc.go `emitRef` 同码续 run 仅 Count++，put/reset 天然打断；TestRLEExpansion
4 失败不留痕：api.go `New`/`Append` 先验后改，enc.go `Decode` 只累积局部切片；TestRejectedOpsNoTrace
探测计数：dict.go `probes` 非导出、哈希 O(1)，TestProbeCountO1；并发：api.go RWMutex，TestConcurrentDecode
