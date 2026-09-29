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

## 分代复制式垃圾回收器（`gengc` 包）

`gengc` 包在模拟堆上实现分代复制式 GC，源码位于 `gengc/`。

### 分代布局

- **年轻代**：一个分配区 `Eden` + 两个等大幸存区 `From`（存活）/ `To`（空闲）。
- **老年代**：`Old`，存放晋升对象；每次回收顺便在老年代内部做一次整理（复制到全新缓冲区），因此“各代已用字节 == 代内存活对象字节之和”恒成立。
- 对象定长布局：14 字节头部（句柄 ID 4B、存活次数 age 2B、引用字段数 4B、载荷长度 4B）+ `nfields*8B` 引用字段 + 载荷。
- 对外只暴露**句柄**（`uint32`，0 表示空引用），句柄表把 ID 映射为堆内位置 `(region, offset)`；对象移动后改写表项，被回收的句柄删除键。
- 根集由调用方用 `AddRoot/RemoveRoot` 显式登记。

### 复制与转发规则

1. `Eden` 剩余空间放不下新对象时，先自动触发一次次要回收；也可显式 `MinorGC`。
2. 回收起点 = **根集 ∪ 记忆集**（老 -> 幼）。采用 Cheney 式宽度优先扫描：
   - 每个可达年轻对象复制到 `To`；复制后在**源对象首字节留转发地址**，`fwd[src]=dst` 保证同一对象只复制一次（跨代环也安全）。
   - 扫描新副本字段时，把其中的源指针通过转发地址改写为新位置；根句柄与老年代内指针同样改写。
   - 存活一次 `age++`。`age >= PromoteAge` 即晋升到 `Old`；`To` 放不下该对象时**提前晋升**。
   - 老年代可达对象复制到新 `Old` 缓冲区（整理）。
3. 提交：`To` 上位为新的 `From` 并把标签从临时 `To` 改标为存活区，Eden/旧 From 整体丢弃；未被复制的对象不可达，年轻垃圾所在 Eden 被整体释放。
4. 若晋升时 `Old` 空间不足，丢弃所有新缓冲区并**恢复回收前快照**（Eden/From/Old 逐字节、句柄表、记忆集、计数全部不变），返回 `ErrOldGenFull`。回收中所有写入都先落在新缓冲区，天然保证原子撤回。

### 记忆集维护推导

次要回收只扫描年轻代，但老年代字段可能持有年轻代指针，单纯从根闭包会漏掉“只被老年代引用”的年轻对象。维护规则：

- **写屏障**：对老年代对象写字段时，若目标是年轻代对象，把该老对象偏移加入记忆集 `remembered`（年轻代对象之间、以及年轻 -> 老方向的写都不需要记录）。
- **晋升即登记**：晋升对象复制完成后要扫描其字段；若仍引用年轻代对象，下次回收必须能发现它——实现上每次回收提交时**线性扫描整理后的老年代重建记忆集**，凡字段指向年轻代即保留，因此晋升对象引用年轻代自动入集。
- **失效即移出**：重建时只保留当前确实指向年轻代的老对象；被清空、目标也已晋升、或老对象自身死亡的条目自然消失，无需显式删除。
- 为保证“同一操作序列得到相同回收/晋升次数”，记忆集、根集、句柄的遍历一律按键升序，Cheney 队列为确定性 FIFO。

记忆集只提供额外起点，不改变可达性定义：从“根 ∪ 记忆集”出发经引用闭包得到的年轻对象集合，恰好等于全堆朴素可达性中的年轻可达对象（老对象若不可达，其持有的年轻指针会被当成浮动垃圾在下一轮清掉）。

### 可区分的拒绝原因

| 错误哨兵 | 触发条件 |
| --- | --- |
| `ErrHandleReclaimed` | 访问已被回收（或从未存在）的句柄 |
| `ErrFieldIndex` | 引用字段下标越界（含负数） |
| `ErrOldGenFull` | 老年代放不下晋升对象；本次回收整体撤回 |
| `ErrPayloadTooLarge` | 对象含头部超过 Eden 容量，或载荷扩写超出已分配长度 |

### 并发语义

`Heap` 内部用 `sync.RWMutex`：读写/分配/回收互斥，读操作共享锁。回收期间任何调用要么看到回收前状态，要么看到提交后状态，看不到中间态。计数通过 `MinorGCCount/PromotionCount` 暴露。

### 本地验证

```bash
# 全量测试
go test ./gengc/ -v

# 竞态检测 + 重复执行
go test -race -count=3 ./gengc/

# 格式化与静态检查
gofmt -l .
go vet ./...
```

测试覆盖（每个用例均通过 `Heap.Logger` 打印输入、输出与判定依据）：

- `TestOnlyReferencedByOldSurvives`：只被老年对象引用的年轻对象经记忆集存活。
- `TestPromotedObjectReferencesYoung`：晋升对象仍引用年轻对象时自动进入记忆集，后续回收指针正确改写。
- `TestCrossGenerationalCycle`：老 -> 幼 -> 老跨代环整体存活且指针图同构。
- `TestSurvivorOverflowPromotesEarly`：幸存区放不下时提前晋升。
- `TestPromotionFailureRollsBack`：晋升失败返回 `ErrOldGenFull`，堆与回收前逐字节相同。
- `TestRejectedReasons`：四类拒绝原因逐一命中。
- `TestVsNaiveReachability`：与全堆朴素可达性参照模型逐轮对照存活集与引用图，并校验各代字节不变量。
- `TestRememberedSetPruned`：跨代引用消失后记忆集条目移出。
- `TestConcurrentAccess`：分配/读写/回收并发下 `-race` 干净，结束后不变量成立。
- `TestDeterministicCounts`：同一操作序列两次重放，回收/晋升次数与对象位置完全一致。
