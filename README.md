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

## 离线待投递消息箱（`mailbox` 包）

`mailbox` 实现离线设备的待投递消息箱：按折叠键替换、溢出整体丢弃并合并为
溢出标记、总存量逐条淘汰、存活期过期与优先级出队。构造：

```go
mb, err := mailbox.New(K, P, Lmax) // K∈[1,64]，P∈[1,1e4]，Lmax∈[1,1e4]（不含标记）
```

### 入箱判定次序（Enqueue）

1. 拒绝检查（只报第一个，且不改变任何状态与最大 now）：参数非法
   （`ErrInvalidParam`）→ 时钟回退（`ErrClockRollback`）→ 与存活消息 id 重复
   （`ErrDuplicateID`，仅 Enqueue，已过期未清除者不算）。
2. 清除箱内全部 `exp <= now` 的消息（静默，不计入标记）。
3. `ttl == 0`：不存放，返回 `Dropped`。
4. `ck` 非空：已有同 ck 消息则替换（旧消息移除，新消息取新序号），返回
   `Collapsed`；否则若不同折叠键数已等于 K，发生折叠溢出——箱内全部可折叠
   消息连同新消息一并丢弃，`c = 可折叠数 + 1`，返回 `Overflow`；否则存入。
5. `ck` 为空：不可折叠消息数已等于 P 时发生不可折叠溢出，`c = P + 1`；
   否则存入。两类溢出互不波及，高优先级消息同样被丢弃。
6. 仅当以 `Stored` 存入后总数大于 Lmax：淘汰一条——有普通类则淘汰普通类中
   序号最小者，否则淘汰高类中序号最小者；若正是新消息则结果改为 `Evicted`。

### 标记计数与序号

- 全局序号 seq 从 1 起：每次存入或替换一条消息、每次创建或更新标记各取一个
  新序号；`Dropped` 与被溢出丢弃的新消息不取序号。
- 溢出或淘汰（`c = 1`）时：标记已存在则 `n += c` 并取新序号，否则创建
  `n = c`。过期清除不计入标记。标记被 Drain 取走后再次溢出会重新创建。

### 出队顺序（Drain / Peek）

- `Drain(now, cnt)` 先清除过期消息，再按「高优先级消息与标记为一类、普通
  消息为另一类，高类在先，类内按序号升序」取前 cnt 项；标记作为一项返回其
  n 并移除。
- `Peek(now)` 只读返回存活项的同一顺序，不清除、不推进最大 now。

### 本地验证

```bash
# 全部单元测试（含题目示例走查与 2000 组随机序列对拍，-v 打印输入/输出/判定依据）
go test ./mailbox/ -v

# 竞态检测（并发调用等价于某个串行顺序）
go test -race ./mailbox/
```
