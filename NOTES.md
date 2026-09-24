# NOTES (ontology-330: CDC partial-column update compaction)

## 一、六步分步表（N 表示显式 NULL；初始行 k: a="1",b="x",c=NULL）

| i | 合并后 Set | 合并后 Before | 此刻实际行 (a,b,c) |
|---|---|---|---|
| 1 | a:"2" | a:"1" | ("2","x",N) |
| 2 | a:"2",b:N | a:"1",b:"x" | ("2",N,N) |
| 3 | a:"3",b:N,c:"z" | a:"1",b:"x",c:N | ("3",N,"z") |
| 4 | a:"3",c:"z"（b 因 "x"=="x" 剔除） | a:"1",c:N | ("3","x","z") |
| 5 | a:"3"（c 因 N==N 剔除） | a:"1" | ("3","x",N) |
| 6 | a:N | a:"1" | (N,"x",N) |

最终输出：一条 Update，Set={a:NULL}，Before={a:"1"}。

- (甲) 缺席误写成 NULL：第1步得 ("2",N,N)，正确为 ("2","x",N)；第6步得 (N,N,N)，正确为 (N,"x",N)。
- (乙) 同列保留先到值：Set={a:"2",b:N,c:"z"}，Before={a:"1",b:"x",c:N}；下游行 ("2",N,"z")，正确 (N,"x",N)。
- (丙) Before 取最后一次：Set={a:N,b:"x",c:N}，Before={a:"3",b:N,c:"z"}；下游行 (N,"x",N) 恰好与正确相同，但违反不变量 2（Before 不等于批前值 a="1",b="x",c=N，b、c 本应被剔除）。

## 二、四条不变量的保证位置与钉住测试

1. 与朴素参照一致：`cbatch.Apply` 全程维护逐事件更新的影子行、只在全部校验通过后提交（cbatch.go）；钉住：`TestRandomBatchesMatchNaive`（api_test.go，含缺席/N/"")。
2. Before 镜像正确：`Merger.Merge` 中 Before 仅在列首次出现时写入 firstBefore，`Result` 输出时剔除 Set==Before 的列并按 Set 列集镜像 Before（pcol.go）；钉住：`TestBeforeMirror`（api_test.go）。
3. 至多一条且按首次出现顺序：`cbatch.Apply` 的 order 切片与 per-key 单一 Merger（cbatch.go）；钉住：`TestOutputOrder`（api_test.go）。
4. 失败不留痕：任何错误在提交前返回，pending 影子行被丢弃，原表不触碰（cbatch.go）；钉住：`TestRejectedBatchNoTrace`（api_test.go）。
