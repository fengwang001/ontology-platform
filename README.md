# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 内容寻址去重块库（`blockstore` 包）

块按内容摘要寻址；写入会话先上传缺失块（已存在则去重复用），最后原子提交一份快照清单。
回收采用经典的 **mark-then-sweep 两轮协议**，可与持续进行的备份写入并发执行。

### 块状态机

每个块在任意时刻恰处于三种状态之一：

- `normal`：可正常读写；要么被某份已提交清单引用，要么尚未经过回收判定。
- `pending`（待删）：第一轮回收判定“当前无任何已提交清单引用”，但**不删除、仍可完整读回**。
- `deleted`（已删）：第二轮回收物理删除，此后读取返回 `ErrBlockMissing`。

### 两轮回收协议

1. **第一轮（标记 + 快照见证会话）**
   - 计算引用集合 `R` = 所有已提交清单引用块的并集。
   - 将“`normal` 且不在 `R` 中”的块置为 `pending`。
   - 同时快照此刻所有未结束的会话 id，记为见证集合 `W`。
2. **第二轮（闸门 + 复核删除）**
   - 闸门条件：`W` 中所有会话都已结束。只要还有一个未结束，本轮**什么都不删**，
     待删块继续可读，调用方稍后重试第二轮即可。
   - 闸门通过后重新计算 `R`：
     - 仍不在 `R` 中的 `pending` 块 → 物理删除（`deleted`）。
     - 已被新清单引用的 `pending` 块 → 恢复为 `normal`。

### 安全性推导（为什么任何已提交快照永远完整）

考虑第二轮要删除的某个块 `b`，设第一轮时刻为 `t1`、第二轮删除时刻为 `t2`。

- 第一轮时 `b ∉ R(t1)`，且 `W` 覆盖 `t1` 时刻所有未结束的会话。
- 闸门要求 `W` 中每个会话在 `t2` 前都已结束。任何在 `t1` 之后**新开始**的会话，
  其上传/提交发生在第一轮标记之后：若它复用了 `b`，上传路径会立即把 `b` 恢复为
  `normal` 并移出本轮待删集合；若它提交的清单引用 `b`，提交路径同样立即恢复 `b`。
  因此新会话不需要进入 `W`——恢复规则已经覆盖。
- 反证：若 `t2` 时存在一份已提交清单引用 `b`，则该清单要么在 `t1` 前提交
  （与 `b ∉ R(t1)` 矛盾），要么由 `W` 中的会话提交（该会话必已结束，而“先传块、
  后提交”的协议使提交成功的前提是块存在，提交时恢复规则已把 `b` 变回 `normal`），
  要么由 `t1` 后的新会话提交（恢复规则同样生效）。三种情况都不可能走到删除分支。
- 结论：**被删除的块在删除时刻不被任何已提交清单引用，且删除后不会再出现引用它的
  清单**（提交会校验块存在性）。故每份已提交清单在任意交错下都能完整读回。

### 待删块恢复规则

- 会话 `Upload` 命中一个 `pending` 块（内容去重复用）→ 立即恢复为 `normal`，
  并从当前回收轮次的待删集合中移除，不增加容量占用。
- `Commit` 的清单引用了 `pending` 块 → 校验通过、清单落盘的同一临界区内恢复为
  `normal` 并移出待删集合。
- 第二轮复核时发现待删块已在引用集合中 → 恢复为 `normal`（防御性兜底）。

### 提交拒绝（可区分原因，且无副作用）

提交在写入任何状态前完成全部校验，拒绝时不留清单、不改任何块状态：

- `ErrBlockMissing`：引用了未上传且库中不存在（或已删除）的块。
- `ErrSessionNotFound`：会话不存在。
- `ErrSessionClosed`：会话已结束。
- `ErrAlreadyCommitted`：同一会话重复提交（清单 id 冲突也归为此类）。
- 上传新块超出容量时返回 `ErrCapacityFull`，被拒上传不留块、不占容量。

### 活性保证（无引用块最终会被删）

所有公共操作由同一把 `RWMutex` 线性化，临界区均为有限操作，不会死锁。
对于一个始终无人引用的块：一旦某次第一轮发生，该块进入待删集合；当且仅当见证集合
中的会话全部结束后第二轮才执行删除。若某个见证会话长期不结束，第二轮会反复跳过——
这是**正确性优先于回收**的有意设计（该会话仍可能提交引用待删块的清单）。在“每个会话
最终都会结束”这一标准公平性前提下，下一轮两轮回收必然删除该块，故垃圾块最终消失。

### 确定性

删除/恢复结果只依赖引用集合与见证集合的成员关系；对外返回的删除、恢复、见证列表均按
摘要或会话 id 排序。同一操作序列与同一交错顺序必然得到相同的删除集合。

### 本地验证

```bash
# 全量测试（含竞态检测，建议多跑几轮）
go test -race -count=10 ./...

# 详细日志：每条操作都打印 输入(in) / 输出(out) / 判定依据(decision)
go test -run TestLogsContainInputOutputDecision -v ./blockstore

# 关键场景用例
go test -race -v -run 'Round1OpenSession|UploadReusesPending|LongSessionBlocks|UnreferencedDeleted|CapacityFull|ConcurrentInterleaving|Deterministic' ./blockstore

gofmt -l . && go vet ./...
```
