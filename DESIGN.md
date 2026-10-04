1. 三包：rule（规则集与版本）、quarantine（容量/队列/丢弃账，无判定逻辑）、flow（闸门编排）。
2. 数据模型：记录 Entry{Seq,Key,Fields,RV,Violations,State}；每键一个队列（环形切片+head），
   队首恒为 Quarantined（存最近一次判定 rv/违规），其后恒为从未判定的 Held。
3. 保序取舍：同键后继一旦发现队列非空即直接 Held、零判定（evals=0），放弃"各自判定、合格即放行"，
   因为那会让后继越过先到的被隔离者，无法复现"阻塞→重判→放行"次序。
4. 规则变更只 bump rv，不自动重判（放弃全量重判：与 Held 条数相关、且批量规则变更会引发级联判定）；
   重判惰性发生在 Reeval/ReevalAll/Fix 及 Fix/Release/Discard 后的隐式 Reeval。
5. 判定只在队首逐条进行：放行 k 条则判定 k 或 k+1 次，与 Held 数量无关，满足 evals 复杂度界。
6. seq 仅在"被接受"时分配：直接放行记 Out，入队（含 Held）记 Seq；被拒操作不占号。
   容量满只挡住需要入队者，合格直放记录不受容量影响；错误序：非法>Banned>KeyFull>Full。
7. Release 强制放行：Forced=true，违规列表取队首最近一次判定快照（不再判定）；Discard 入 Dropped，
   累计 Dmax 次封禁，封禁只挡新 Ingest，不影响存量处置。
8. IngestBatch 先用影子逐条模拟（不入真状态、不增 evals/seq），任一条失败按最小下标整体拒绝；
   全部可接受才按序提交。模拟与提交共用同一份逐条入队逻辑，避免两套语义漂移。
9. 并发：flow 单把 sync.Mutex 串行化全部状态变更（结果等价某串行序）；rule.Set 自带独立锁。
   锁序固定 gate→rules（eval 在持 gate 锁时进行），rules 绝不回调 gate，无死锁；确定性优先于吞吐。
10. 判定依据：在 rv 快照下按 rule id 升序求值；字段缺失或越界即违规；任一 Block 即不通过。
11. 错误哨兵：ErrInvalidParam/ErrBanned/ErrKeyFull/ErrFull/ErrNoQueue 与 ErrRuleNotFound，
    均 errors.Is 可判定；BatchError{Index,Err} 包裹批内首拒原因。
12. 本地验证：go build ./...；go test -race -v ./rule ./quarantine（表驱动）；
    go test -race ./flow（示例走查、容量/封禁/批次边界、1500 组随机序列对照朴素模拟，-v 打印输入/输出/判定依据）；
    go vet ./...；gofmt -l . 为空。
