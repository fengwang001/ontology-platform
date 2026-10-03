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

## htm：硬件事务内存锁消除回退控制器

`htm` 包实现一个尽力而为（best-effort）的 HTM 锁消除回退控制器：多个线程的临界区先推测执行，
按读写集冲突、缓存组容量与重试预算判定中止原因，并决定是否回退到全局锁。

### 构造参数

`htm.Config`：`N`（线程数 1..16）、`S`（缓存组数 1..8，地址 `a` 属于组 `a mod S`）、
`W`（每组路数 1..4）、`A`（缓存行地址数 1..64）、`R`（重试上限 0..8）、
`SK`（跳过长度 0..8）、`F`（连败阈值 1..8）。任何参数越界则整体拒绝（`ErrInvalidConfig`）。

### 中止原因

- `Conflict`：访问与其他推测线程的读写集相交（写 vs 读/写、读 vs 写），被中止方 `r` 加一。
- `Capacity`：推测线程某组内读集并写集的不同地址数超过 `W`，同时 `skip` 置为 `SK`。
- `LockHeld`：其他线程走回退拿到全局锁，所有推测线程被中止；不计入 `r`。

### Lock 判定顺序

`Lock(t)`（`t` 须空闲或已中止）依次判定，结果中的 `Rule` 字段记录判定依据：

1. 锁持有者非空：返回等待，不改变任何状态；
2. `t` 已中止且原因是容量：直接走回退（不消耗 `skip`）；
3. `skip > 0`：`skip` 减一并走回退（不计 `fb`）；
4. `r > R`（冲突重试预算耗尽）：走回退；
5. 否则转推测，读写集清空。

走回退即 `t` 成为持有者，其余所有推测线程以 `LockHeld` 中止（按线程号升序返回）。
经规则 2 或 4 的回退令 `fb` 加一，达到 `F` 时 `skip` 置为 `SK` 且 `fb` 清零。

### Access / Unlock

`Access(t, a, write)`：`t` 须推测或回退；回退线程的访问无任何影响。推测线程先中止所有冲突方
（升序返回），再记录地址，最后检查组容量，超 `W` 则自身容量中止并置 `skip = SK`。

`Unlock(t)`：推测则提交（清空集合、`fb` 清零），回退则释放持有者；二者都清 `r`。
其余状态拒绝。拒绝原因按序只报第一个：线程号越界 → 状态不符 → 地址越界（仅 Access）；
被拒绝的调用不改变任何状态。

### 并发与可复现性

所有方法由单把互斥锁串行化，并发调用等价于某个串行顺序；相同调用序列重放得到完全相同的
结果与计数。不变式：推测线程两两无冲突、持有者非空时无推测线程、每组不同地址数不超过
`W`、`0 <= skip <= SK`、`0 <= fb < F`。

### 本地验证

```bash
# 单元测试（规则逐条覆盖）
go test ./htm/

# 查看逐步日志（输入、输出与判定依据）
go test -v -run TestRandomDifferential ./htm/

# 2000 组随机序列与朴素模拟对照 + 竞态检测
go test -race ./htm/
```
