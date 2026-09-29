# slottedpage：槽式记录页管理器

在一个固定字节大小的页内插入、更新、删除变长记录；必要时**就地整理碎片**，
整理前后槽编号（记录编号）保持稳定，页内字节账目始终与逐项累计一致。

## 页内布局

地址自低向高依次为：

| 区间 | 范围 | 说明 |
| --- | --- | --- |
| 页头 | `[0, headerSize)` | 前 28 字节元数据，其余保留为零 |
| 槽目录 | `[headerSize, dirEnd)` | 每项 `slotSize`，自前向后增长 |
| 连续空闲区 | `[dirEnd, freeEnd)` | 目录与记录之间，未用字节恒为零 |
| 记录区 | `[freeEnd, pageSize)` | 记录紧挨，自页尾**向前**放置 |

- `dirEnd = headerSize + slotCount*slotSize`
- 页头（大端）：魔数 `SPG1`(4B)、`pageSize`、`headerSize`、`slotSize`、
  `slotCount`、`freeEnd`、`compactions`，各 4B。
- 槽项：`offset`(4B) + `length`(4B)；`length==0` 即空槽（此时 `offset==0`）。

## 空间账目公式

```
记录字节和 = Σ 非空槽的 length
可用字节   FreeBytes  = pageSize - headerSize - slotCount*slotSize - 记录字节和
连续空闲区 ContigFree = freeEnd - dirEnd
```

恒有 `FreeBytes >= ContigFree`，差额即删除中间记录后记录区内的碎片。
空页容量（判断“单记录带一个槽项是否过大”）为 `pageSize - headerSize`。

## 插入与槽复用

1. 从槽 0 起扫描，**复用编号最小的空槽**；没有空槽才追加新槽项。
2. 新记录放在 `freeEnd - len` 处，即紧挨当前最低记录向前。
3. 连续空闲区 `>= len(data) (+ 追加新槽时再加 slotSize)` 时**绝不整理**。
4. 连续区不够、但全页 `FreeBytes`（含碎片）够时：在整页副本上把所有存活
   记录与新记录一起重排——编号越小越靠页尾、彼此紧挨——成功后一次性替换
   页映像，`compactions += 1`。
5. 全页可用字节仍不足：拒绝，页字节不变。

## 变长更新

- 等长：原地覆盖。
- 缩短：记录向后对齐（贴住后方记录），释放的前导字节清零并并入空闲/碎片。
- 增长：该记录恰为最低记录（`offset == freeEnd`）且前方连续区够时，原地
  前移扩展；否则与插入相同——全页可用字节够则整理（新内容参与重排），
  不够则拒绝且页不变。

## 删除与槽回收

- 删除只把槽项置空并清零记录字节，**不移动任何记录**。
- 若被删槽位于目录末尾，则连同其前方连续的空槽一并收回，目录收缩；
  位于中间的空槽保留，等待后续插入复用。

## 拒绝原因与优先级

多因并存时只返回最先命中的一个：

1. `ErrEmptyRecord`：记录为空
2. `ErrRecordTooLarge`：单条记录连同一个槽项超过空页容量
3. `ErrInvalidSlotID`：编号越界
4. `ErrEmptySlot`：编号指向空槽
5. `ErrInsufficientSpace`：可用空间不足

校验失败时不分配槽、不写字节；整理在副本上完成，失败不提交，因此被拒绝
操作前后的页映像逐字节相同。

## 并发与确定性

- `sync.RWMutex`：读操作（`Get`/`Stats`/`Marshal`）并发，写操作互斥。
- 读者不可能观察到整理中的半移动状态（副本构造 + 单次指针替换）。
- 未使用字节一律清零；同一操作序列得到**逐字节相同**的映像，
  `New(cfg, image)` 可还原出完全相同的页（含 `compactions` 计数），
  还原时校验魔数、几何参数、槽项区间与记录重叠，损坏映像被拒绝。

## 本地验证

```bash
# 若 go 不在 PATH（本仓库环境在 /usr/local/go/bin）
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/tmp/gocache-ontology   # 家目录构建缓存为只读时需要

go test -race -v ./slottedpage          # 竞态检测 + 逐条输入/输出/判定日志
go test -cover ./...                    # 当前覆盖率约 90%
gofmt -l . && go vet ./...
```

测试覆盖：删中间记录后插大记录触发整理、末尾空槽连带收回、复用最小空槽、
恰好填满与差一字节、变长更新失败后原样、错误优先级与页字节不变、
映像确定性/还原/损坏拒绝、以及 8 读者 + 4 写者 + 1 封送者的并发压测。
