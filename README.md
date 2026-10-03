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

## mailbox：离线设备待投递消息箱

`mailbox` 包实现离线设备的待投递消息箱。构造参数为折叠键种类上限
`K`（1..64）、不可折叠条数上限 `P`（1..10^4）与消息总存量上限 `Lmax`
（1..10^4，不含标记）。全部方法持有互斥锁，可并发调用，结果等价于某个
串行顺序；相同操作序列重放得到完全相同的结果与顺序。

### 入箱处置次序

`Enqueue(id, ck, prio, ttl, now)` 先做拒绝检查（按序只报第一个：
参数非法 > 时钟回退 > id 与存活消息重复），被拒绝的操作不改变任何状态；
通过后依次：

1. 静默清除全部 `exp <= now` 的过期消息（`exp = now + ttl`，不计入标记）；
2. `ttl == 0`：不存放，结果 `Dropped`，结束；
3. `ck` 非空：同 `ck` 已存在则替换（旧消息移除，新消息取新序号），结果
   `Collapsed`；否则不同折叠键种类数已达 `K` 则折叠溢出——箱内全部可
   折叠消息连同新消息一并丢弃，`c = 可折叠消息数 + 1`，结果 `Overflow`；
   否则存入，结果 `Stored`；
4. `ck` 为空：不可折叠条数已达 `P` 则不可折叠溢出——全部不可折叠消息
   连同新消息一并丢弃，`c = P + 1`，结果 `Overflow`；否则存入；
5. 仅当以 `Stored` 存入后总数大于 `Lmax`：淘汰一条——有普通类则淘汰
   普通类中序号最小者，否则淘汰高优先级类中序号最小者（可能就是刚存入
   的新消息，此时结果改为 `Evicted`），`c = 1`，标记序号在新消息序号
   之后取。

两类溢出互不波及，高优先级消息也在各自类别的溢出中一并丢弃。

### 标记计数与序号

发生溢出或淘汰时：标记已存在则 `n += c` 并取新序号，否则创建 `n = c`
并取新序号。全局序号从 1 起，每次存入或替换一条消息、每次创建或更新
标记各取一个新序号。箱内至多一条标记；标记被 Drain 取走后再次溢出会
重新创建、`n` 重新累计。过期清除与折叠替换不计入标记。

### 出队顺序

`Drain(now, cnt)` 先清除过期消息，再按「高优先级消息与标记为一类在先、
普通消息为另一类在后，类内按序号升序」取前 `cnt` 项；标记作为一项返回
其 `n` 并移出箱。`Peek(now)` 只读返回当前存活项的同一顺序，不清除任何
消息，与随后的 `Drain` 一致。

### 本地验证

```bash
# 全部单测（规则用例 + 两个规格示例 + 2000 组随机序列与朴素模拟对拍）
go test ./mailbox/

# 打印对拍日志（每步输入、输出与判定依据）
go test ./mailbox/ -run TestRandomDifferential -v

# 竞态检测
go test -race ./mailbox/
```
