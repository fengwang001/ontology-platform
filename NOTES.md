# VLQ/LEB128 推导

## 八值无符号编码表（7 位组自最低有效组起）

| 值 | 7位组(低→高) | 续位 | 字节序列 | n |
|---|---|---|---|---|
| 0 | 0 | 0 | `00` | 1 |
| 1 | 1 | 0 | `01` | 1 |
| 127 | 127 | 0 | `7F` | 1 |
| 128 | 0,1 | 1,0 | `80 01` | 2 |
| 300 | 44(0x2C),2 | 1,0 | `AC 02` | 2 |
| 16383 | 127,127 | 1,1→0 | `FF 7F` | 2 |
| 16384 | 0,0,1 | 1,1,0 | `80 80 01` | 3 |
| 2097151 | 127,127,127 | 1,1,0 | `FF FF 7F` | 3 |

(甲) 错误实现（每轮都置续位、v==0 后补 `00`）：128 → `80 81 00`；300 → `AC 82 00`。正确分别为 `80 01`、`AC 02`。错在最后一组数据仍被 `|0x80`（`01→81`、`02→82`），循环后又无条件补写冗余终止字节 `00`；多出的字节即末尾 `00`（且前一字节被污染）。
(乙) 不查规范性解 `80 00`：值得 0、消耗 2 字节。按规范应返回哨兵 `enc.ErrNonCanonical`、游标不推进；0 的唯一最小编码是单字节 `00`。
(丙) 有符号当无符号不解符号扩展：单字节 `40` 得 64；按规范 bit6=1 需符号扩展，正确为 **−64**。`C0 00` 终止字节是 `00`（bit6=0），余数为 0、值 64 为正，本就无需符号扩展，故两种理解都得 64，不受影响（64 的规范编码正是 `C0 00`，−64 才是 `40`）。

## 四条不变量（保证位置 / 钉住测试）

1. 往返一致：`enc/enc.go` 的 `EncodeUint/EncodeInt` 与 `DecodeUint/DecodeInt`（7 位组对称、算术右移+符号扩展）；`enc_test.go::TestRoundtrip`（表驱动含全边界值与随机值）。
2. 规范性固定点：`enc/enc.go` DecodeUint 末组为 0、DecodeInt 末组 `00/7F` 冗余判定返回 `ErrNonCanonical`；`enc_test.go::TestCanonical`（含 `80 00`、`FF 7F`）。
3. 与朴素参照字节一致：`enc/enc.go::EncodeUint` 即朴素逐组实现；`enc_test.go::TestNaiveReference`（测试内另写教科书实现，逐向量比对）。
4. 失败不留痕：`stream/stream.go::Reader.ReadUint/ReadInt` 先解码成功才更新 `pos`，出错直接返回；`stream_test.go::TestRejectNoAdvance`。单趟线性由 `stream_test.go::TestSinglePassLinear` 钉住，并发原子写由 `stream_test.go::TestConcurrentWriter` 钉住。
