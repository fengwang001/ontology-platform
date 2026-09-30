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

## 槽式记录页管理器（`slottedpage` 包）

在固定字节大小的页内插入、更新、删除变长记录，并在必要时就地整理碎片。
记录编号（即槽下标）在整理前后稳定不变。

### 页内布局

```
偏移 0
├─ 页头（headerSize 字节，创建时给定，>= 12）
│    ├─ [0:4)   槽数 uint32（管理器维护）
│    └─ [4:12)  整理次数 uint64（管理器维护）
├─ 槽目录（紧跟页头向高地址增长，每项 slotSize 字节，>= 8）
│    └─ 每项：[0:4) 记录偏移 uint32，[4:8) 记录长度 uint32；长度为 0 表示空槽
├─ 连续空闲区（槽目录末尾 dirEnd 与最低记录 lowPtr 之间）
└─ 记录区（新记录紧挨当前最低记录向低地址放置，直到页尾）
页尾（pageSize）
```

### 空间账目公式

```
可用字节 Available   = pageSize - headerSize - slotCount*slotSize - Σ存活记录字节
连续空闲 Contiguous  = lowPtr - (headerSize + slotCount*slotSize)
```

页内字节账目始终与逐项累计一致，每步操作后均可按上式核验。

### 整理（compaction）触发条件

- 仅当「连续空闲区不够、但可用字节够」时才就地整理；连续空闲区足够时绝不整理。
- 整理时存活记录按编号升序从页尾向低地址紧挨重排：编号越小的记录越靠页尾，
  彼此紧挨；槽内偏移同步改写，记录编号不变，整理次数加一并对外报告
  （`Compactions()`）。
- 变长更新原地放不下时走同样的整理路径；整理后仍不够（可用字节不足）则
  整体拒绝，页的任何字节不变。

### 槽分配与回收规则

- 插入优先复用编号最小的空槽；无空槽才在目录末尾新增槽项（多记一项槽字节）。
- 删除只把槽置空（偏移、长度清零）；若被删槽位于目录末尾，则连同其前方
  连续的空槽一并收回，槽目录缩短、槽字节归还可用空间。

### 拒绝原因（多因并存时按此顺序只报第一个）

1. `ErrEmptyRecord`：记录为空；
2. `ErrRecordTooLarge`：单条记录连同一个槽项超过空页容量
   （`len(rec)+slotSize > pageSize-headerSize`）；
3. `ErrInvalidRecordID`：编号越界或指向空槽；
4. `ErrInsufficientSpace`：可用空间不足。

被拒绝的操作整体生效前即返回，不改变页的任何字节。

### 并发与确定性

- 读写可并发调用：写操作持写锁，读操作（`Get`/`Image`/`Available` 等）持
  读锁，整理在写锁内完成，读者不会看到半移动状态。
- 同一操作序列得到逐字节相同的页映像；`Image()` 导出的映像可用
  `Restore(image, headerSize, slotSize)` 还原出完全相同的页（含槽数与
  整理次数）。

### 本地验证

```bash
# 全部用例（日志打印输入、输出与判定依据）
go test -v ./slottedpage

# 带竞态检测（覆盖并发读写用例）
go test -race ./slottedpage

# 指定场景
go test -run TestCompactionAfterMiddleDelete -v ./slottedpage
```
