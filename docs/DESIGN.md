# 权限决策审计回放模块 — 设计说明

## 目标与范围

模块为本体平台提供四件事：

1. 每次实际访问判定生成一条不可变审计记录（主体、目标实例、请求内容、判定时刻、判定结果、当时的权限规则版本标识）。
2. 权限规则以仅追加的版本链保存，旧版本完整保留、顺序唯一确定。
3. 可按审计记录登记的版本标识**无缓存**重建历史裁决，并把重建结果与原结果比较，区分「版本内容被篡改」与「原判过程有缺陷」。
4. 对历史误判只能追加独立纠正记录；查询历史合法性时给出三态答案，且结论与查询时刻无关。

代码布局：

- `audit/`：生产实现（`service.go`、`store.go`、`hash.go`、`engine.go`、`integrity.go`、`log.go`、`filelog.go`、`types.go`）。
- `naive/`：独立维护的朴素参照实现，刻意不与 `audit` 共享任何领域代码。
- `audit/*_test.go`：单元测试与针对 `naive` 的随机差分对照测试。

## 数据模型

### 规则版本（`RuleVersion`）

- `ID`：调用方提供、全库唯一。
- `Seq`：提交时分配的从 1 开始的单调序号。
- `ParentID`：必须等于当前链头（首个版本必须为空）。由此并发提交天然串行化：同一时刻只有一个版本能成为链头，冲突提交报 `ErrInvalidInput`，不产生半截版本。
- `ContentHash = SHA256(规范化JSON(ID, ParentID, Rules))`：精确绑定「该版本的规则内容」。规则先按 `(Order, ID)` 稳定排序、成员集合排序后再编码，使哈希与提交时的切片顺序无关。
- `ChainHash = SHA256(Seq, ID, ParentID, ContentHash, PrevChainHash)`：把版本钉死在链上唯一位置。

旧版本永不更新、永不删除；所有写操作只调用 `appendVersion`。

### 审计记录（`AuditRecord`）

- `Seq` 单调，`PrevHash` 指向上一条审计记录的 `RecordHash`，形成审计哈希链。
- `RecordHash = SHA256(规范化JSON(全部业务字段 + PrevHash))`，哈希字段自身不参与哈希。
- `RuleVersionID` 是回放的唯一规则来源；`EngineTag` 仅记录原裁决实现出处，回放一律使用规范化的 `CanonicalEngine`。

### 纠正记录（`Correction`）

- 永远是独立的新记录；原始审计的任何字段都不被修改（测试 `TestCorrectionChainAndLegality` 比对纠正前后的 `RecordHash`）。
- `AuditID` 始终指向被纠正的**原始**审计记录；`TargetType/TargetID` 指向直接前驱（原始记录或上一条纠正）。
- 同一原始记录的纠正形成一条**线性链**：新纠正的前驱必须是当前链头，结论必须与链头不同。这保证「多条纠正之间的先后关系唯一且确定」，也使「是否存在尚未被否定的纠正」这一问题只有一个答案——链头。后一条纠正可以否定前一条纠正的结论，自身仍不可变。
- 纠正链另有全局 `Seq/PrevHash` 哈希链，跨不同审计记录也能检测删除/重排。

## 回放语义

`Replay(auditID)` 在单把互斥锁内完成，读取顺序即错误的固定优先顺序：

1. 审计记录不存在 → `ErrAuditNotFound`
2. 记录登记的版本标识不存在 → `ErrVersionNotFound`
3. 版本内容/链哈希校验失败 → `ErrVersionTampered`，结果 `INTEGRITY_VERSION_TAMPERED`
4. 审计记录自哈希校验失败 → `ErrAuditTampered`，结果 `INTEGRITY_AUDIT_TAMPERED`
5. 以 `CanonicalEngine` 对「该版本规则 + 该记录输入」重新裁决：
   - 与原结果相同 → `MATCH`；
   - 不同且两边哈希都完好 → `MISMATCH_DEFECTIVE_ORIGINAL`（原判过程有缺陷，可纠正）。

关键区分：规则版本内容由 `ContentHash` 钉死。回放不一致时，若版本哈希被破坏则判为**完整性破坏**（可与误判明确区分报告）；版本完好则只可能是原判缺陷。回放不查询任何可变「当前规则」「缓存判定」，只用记录里登记的版本与输入。

## 合法性三态

`Legality(auditID)` 完全由已提交状态决定：

- 无有效纠正且原判允许 → `ORIGINAL_ALLOWED`
- 无有效纠正且原判拒绝 → `ORIGINAL_DENIED`
- 链头纠正结论与原判不同 → `CORRECTED`

「被后续纠正否定」在线性链上等价于「不是链头」。即使链头结论恰好回到原判值（c1 翻案、c2 再翻回），`EffectiveAllow == OriginalAllow` 时三态答案仍报原始态；c1 作为被否定的纠正依旧可查。计算过程不读时间戳，因此查询结果不依赖「距最后一条纠正过了多久」。

## 错误分类与优先序

全部错误有一张全局唯一的优先序表（`audit.ErrorPrecedence`），数字小者先报：

| 优先级 | 错误 | 含义 |
| --- | --- | --- |
| 1 | `ErrAuditNotFound` | 被回放/纠正的审计记录不存在 |
| 2 | `ErrVersionNotFound` | 登记的规则版本标识不存在 |
| 3 | `ErrVersionTampered` | 版本内容完整性破坏 |
| 4 | `ErrAuditTampered` | 原始审计记录完整性破坏 |
| 5 | `ErrCorrectionTargetMissing` | 纠正指向不存在的记录 |
| 6 | `ErrCorrectionTampered` | 已有纠正记录完整性破坏 |
| 7 | `ErrCorrectionConflict` | 前驱不是链头/结论未推进 |
| 8 | `ErrReplayMatch` | 回放一致，不允许追加纠正 |
| 9 | `ErrInvalidInput` | 入参非法 |
| 10 | `ErrAlreadyExists` | ID 重复 |

被拒绝的回放或纠正追加发生在任何 append 之前，测试 `TestRejectedAppendChangesNothing` 断言记录数量与哈希均不变。

## 并发

`Service` 用一把 `sync.Mutex` 串行化所有公共方法。由此所有并发历史都等价于某个串行顺序（线性一致）。粒度上这是有意的取舍（见下），临界区内只做内存计算与 SHA-256，无 I/O。并发测试 `TestConcurrentLinearizability` 在 `-race` 下混合 16 个判定/回放/合法性协程与版本提交协程，事后校验序号唯一、哈希链完好。

## 回放读放大的可观测证明

`Store.getVersion` 是计量边界：返回版本的同时返回本次点读的规范化字节数。回放只调用**一次**该接口，因此：

- `ReplayReport.VersionsTouched` 恒为 1；
- `VersionBytesRead` 等于记录登记的那一个版本的规则内容规模（`measureVersionBytes` 对同一份内容独立重算，二者在测试中逐字节断言相等）；
- 与系统中版本总数无关。

证明手段不依赖回放内部实现：计数由存储层在点查边界产出。`TestReplayReadsOnlyNamedVersion` 先对 `v2` 记录基线读数，再追加 60 个大版本（每个 40 条长字符串规则），重放旧记录时读数保持 1 个版本、相同字节；再对大版本重放，读数随该版本自身内容增大。这样同时证明了「不随版本总数增长」与「只随登记版本内容规模变化」。

## 日志

每次公共调用产生一条 `CallLogEntry`：方法名、完整输入、最终输出或分类错误码、`DecisionBasis`（裁决所依据的版本 ID/序号、审计 ID/序号、纠正 ID/序号）。`SliceLogger` 供内存断言，`FileLogger` 每行一个 JSON 落盘。`TestCallLogCompleteness` 校验成功与失败调用都被记录且 JSON 合法、依据字段齐全。

## 关键取舍

- **线性纠正链而非任意 DAG**：任意指向会让「有效纠正」需要图遍历与消解规则，且可能产生多条未被否定的矛盾纠正。线性链 + 「必须接在链头、结论必须翻转」使顺序和当前结论都唯一确定；覆盖「后一条纠正纠正前一条」的需求不留歧义。
- **纠正结论由调用方给出**：再纠正针对的是前一条纠正的人工结论，机器无法自动推出，因此 `CorrectedAllow` 是入参；但首条纠正的前提仍是机器回放得出的缺陷性不一致，防止对一致判定硬加纠正。
- **版本链头由 ParentID 强制**：而不是允许「任意时刻基于任意父版本提交」。后者需要分叉模型和分支选择语义，超出「版本先后唯一确定」的要求。
- **单把锁而非细粒度并发结构**：领域写操作必须全局定序（序号、链哈希），分片锁/无锁结构只会重新实现同一顺序约束；内存裁决极短，单锁最简单且可证明线性一致。
- **回放只做自校验，不遍历版本链**：遍历全链会使读放大随版本总数增长，违背需求。链间链接（`PrevChainHash`）由管理接口 `VerifyAll` 全量核验；回放仅需证明「读到的这个版本内容 == 提交时内容」。
- **内存存储 + 计量接口**：本模块交付领域语义与证明；持久化是适配器问题（实现 `Store` 同款点查/追加接口即可），在此不引入数据库依赖。

## 被放弃的方案

- 用「最后修改时间」判定纠正是否有效：违反「查询结果不依赖距最后纠正多久」，放弃。
- 纠正时原地翻转审计结果并保留「修订标记」：直接违反不可篡改要求，放弃。
- 回放时重新执行所有历史版本以「追链」：读放大 O(版本总数)，放弃。
- 用 map 序列化规则后哈希：Go map 遍历顺序不稳定，且空集与通配语义易混；改为显式排序的规范化 JSON。
- 让 `naive` 直接引用 `audit` 的类型做参照：差分测试会退化成「自己跟自己比」；改为两套独立类型、独立哈希编码、独立错误哨兵，只在测试中按字符串/错误类别对照。

## 本地验证方法

```bash
# 若 go 不在 PATH
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache   # 仅当默认缓存目录只读时需要

go test -race ./...
go test -run TestNaiveDifferential -v ./audit/   # 24 个随机种子 x 400 次操作
go test -run TestReplayReadsOnlyNamedVersion -v ./audit/
go test -coverprofile=cov.out ./audit/
go vet ./...
gofmt -l .
```
