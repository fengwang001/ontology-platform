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

## mailthread：邮件线程归并器

`mailthread` 包按 References 链、In-Reply-To 与回复主题回退规则把乱序
到达的邮件归入线程，线程划分、线程编号与合并事件可精确复现。

### 线程、连通与根

- 节点分两类：**真实**（已登记邮件）与**占位**（只被引用、尚未登记），
  占位同样占用节点名额 N。
- 引用集 L = refs（去重）与非空 inReplyTo 的并集；引用关系构成无向边，
  **线程是节点的连通块**，任何时刻每个线程至少含一封真实邮件。
- **线程根**为线程内真实邮件中 `(ts, id)` 字节序最小者，**线程编号即根
  的 id**，因此更早的邮件到达会更换根并改变线程编号。

### 归并规则（Add）

1. L 非空，或 id 此前是占位：把 id 与 L 的全部节点并入同一线程（原属
   不同线程的全部合并），途径 `ref`，不再走 2、3；
2. 否则若主题是回复主题，在候选线程中选一个并入，途径 `subject`；
3. 否则新建只含该邮件的线程，途径 `new`。

### 主题规范化与候选选择

- 规范化：反复去掉开头的空白与前缀 `re:`、`fw:`、`fwd:`（不分大小写，
  前缀与冒号之间不许有空白）以及「回复:」「回复：」「转发:」「转发：」，
  最后去掉首尾空白，其余原样比较（区分大小写）。至少去掉过一个前缀且
  结果非空的主题为**回复主题**。
- 候选线程须同时满足：根的规范化主题等于本邮件规范化主题、线程内真实
  邮件最小 ts 不大于本邮件 ts、本邮件 ts 减线程内最大 ts 不大于 W
  （差为负时自然满足）。多个候选取线程最大 ts 最大者，并列取线程编号
  字节序小者；无候选则新建线程。
- 候选查找按规范化主题索引，只考察同主题线程，与总线程数无关
  （`TestCandidateScanCount` 用非导出计数器对照总线程 100 与 10000
  两档验证）。

### Gone 口径

`Add` 返回 `(线程编号, 途径, Gone)`。Gone 为本次**涉及的操作前线程
编号**中不等于结果编号者，按字节序升序：规则 1 涉及 L 与 id 的节点原属
线程；规则 2 涉及选中的候选线程。桥接合并与根更换都会使被吞并/被替换
的编号出现在 Gone 中。

### 拒绝与查询

- 拒绝按序只报第一个：参数非法（`ErrInvalidParam`）→ 重复登记
  （`ErrDuplicate`）→ 容量不足（`ErrCapacity`，恰等通过）。被拒绝的
  操作不改变任何节点与线程。
- 查询：`ThreadOf(id)`（真实或占位均可）、`Members(线程编号)`（真实
  成员按 `(ts, id)` 升序）、`Threads()`（编号升序）。
- 所有方法可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序；
  合并按小并大，相同的登记序列重放得到完全相同的线程、编号与 Gone。

### 本地验证

```bash
# 单元测试 + 2000 组随机序列与朴素模拟对拍 + 并发（竞态检测）
go test -race ./mailthread

# 查看对拍日志（每步输入、输出与判定依据）
go test ./mailthread -run TestDifferentialRandom -v

# 候选考察数与总线程数无关的计数器证明
go test ./mailthread -run TestCandidateScanCount -v
```
