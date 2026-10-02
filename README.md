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

## 镜像磁盘回收器（`gc` 包）

`gc.Reclaimer` 实现节点级镜像磁盘回收：层共享记账、两阶段拉取预留、
每仓库保留保护与高低水位回收。所有方法可并发调用，内部以互斥锁串行化，
结果等价于某个串行顺序；相同操作序列重放得到完全相同的
`used`、被删镜像序列与释放字节数。

### 层去重记账

- 层按 ID 全局去重存储，`used` 为被至少一个镜像（含拉取中者）引用的
  不同层字节数之和，任何时刻 `used <= C`。
- 每层维护引用计数；计数归零时层被移除、字节立即释放。
- 同一层 ID 再次出现且字节数不同即层冲突（`LayerConflictError`，带层 ID），
  拉取中镜像的层同样参与比较。

### 两阶段拉取

- `BeginPull` 登记拉取中镜像并立即占用其新增层字节（已存在层只加引用）；
  占用后 `used` 会超过 `C` 则以 `ErrNoSpace` 拒绝且不改状态。
- `CommitPull` 使镜像就绪（`lastUsed=now`、`run=0`）；`AbortPull` 删除
  拉取中镜像并释放其独占层；`Pull` 等价于两者原子完成。
- 并发拉取同一镜像恰有一次成功，其余得到 `ErrImageExists`。

### 保护集与回收

- 触发条件：`used*100 >= high*C`（恰等于也触发），否则返回空结果。
- 回收目标：`need = used - floor(C*low/100)`。
- 保护集在 GC 开始时一次算出：每个 repo（镜像 ID 最后一个 `:` 之前的
  部分，无 `:` 时为整个 ID）的就绪镜像（含运行中，不含拉取中）按
  `lastUsed` 降序、并列按 ID 字节序升序，前 `K` 个受保护。
- 候选：就绪、`run==0`、未受保护且 `now-lastUsed >= minAge`，按
  `lastUsed` 升序、并列按 ID 字节序升序逐个删除；释放字节数
  `>= need` 立即停止；释放 0 字节的镜像照常删除；候选用尽仍不足时
  已删除的保持删除并置 `Insufficient=true`。
- GC 永不删除受保护、运行中或拉取中的镜像。

### 拒绝顺序

参数非法 → 时钟回退 → 镜像已存在 → 镜像不存在 → 拉取中/不在拉取中 →
层冲突 → 空间不足 → 未在运行。只报第一个原因，被拒绝的操作不改变
镜像、层、`used` 与时钟水位。构造参数越界以 `ErrInvalidConfig` 整体拒绝。

### 本地验证

```bash
# 单元测试 + 2000 组随机序列与朴素模拟对照（-v 打印输入/输出/判定依据）
go test ./gc/
go test -race -v ./gc/ -run TestRandomAgainstModel
```
