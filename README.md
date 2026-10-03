# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 邮件线程归并器（`ontology` 包）

`ontology/merger.go` 提供 `Merger`，把乱序到达的邮件按引用链与回复主题归入线程：

```go
m, _ := ontology.New(100, 10000)              // 主题回退窗口 W=100s，节点上限 N=10000
res, err := m.Add(id, refs, inReplyTo, subject, ts)
res.ThreadID // 线程编号；res.Way ∈ {"ref","subject","new"}；res.Gone []string
m.ThreadOf(id)     // 真实邮件或占位节点均可查询，不存在返回 ErrNotFound
m.Members(thread)  // 线程真实成员，按 (ts, id 字节序) 升序
m.Threads()        // 全部线程编号，字节序升序
```

### 线程连通与线程根

- 节点分两类：**真实**（已 `Add` 的邮件）与**占位**（只被 `References`/`In-Reply-To`
  引用、尚未登记）。占位同样占用节点名额。
- 引用集 `L` = 去重后的 `refs`（至多 50 项，重复项视为一项）与非空 `inReplyTo`
  的并集。`Add` 时把 `id` 与 `L` 中全部节点并入同一连通块；连通块即线程。
  只有占位的连通块暂时没有编号，一旦其中任一节点登记为真实邮件即成为线程。
- **线程根**是线程内真实邮件中 `(ts, id 字节序)` 最小者；**线程编号即根的
  Message-ID**。更早（或同 ts 而 id 字节序更小）的邮件到达会更换线程根与编号。
- 存储为并查集（路径压缩 + 按小并大），真实成员集合也按小并大搬移；任何时刻
  每个线程至少含一封真实邮件，线程编号互不相同，节点总数不超过 N。

### 途径选择

1. `L` 非空，或 `id` 此前是占位 → 全部并入同一线程（`ref`），不再走 2、3；
2. 否则主题是回复主题时，在候选线程中选一个并入（`subject`）；
3. 否则新建只含该邮件的线程（`new`）。

### 主题规范化与候选选择

- 反复去掉开头空白与前缀：`re:`/`fw:`/`fwd:`（不分大小写，前缀与冒号之间
  **不许有空白**）、`回复:`/`回复：`/`转发:`/`转发：`（原样匹配），最后去掉
  首尾空白；其余字节原样保留、区分大小写比较。至少去掉过一个前缀且结果非空
  才是**回复主题**。
- 候选线程须同时满足：根的规范化主题与本邮件相同；线程内真实邮件最小
  `ts ≤ 本邮件 ts`；`本邮件 ts − 线程最大 ts ≤ W`（线程最大 ts 更大时差为负，
  满足；W 取等满足）。
- 多个候选取线程最大 ts 最大者；并列取线程编号字节序小者；无候选则走 3。
- 候选查找只扫描该规范化主题下的线程集合（`bySubject` 索引），非导出计数器
  `subjectExamines` 记录逐一核对的线程数，因此考察数不超过同主题线程数、
  与总线程数无关（`TestSubjectExamCounter` 以总线程 100 与 10000 两档对照）。

### Gone 的口径

`Gone` 是**本次涉及的操作前线程编号**中不等于结果编号者，按字节序升序：

- 途径 1：涉及的线程 = `id` 与 `L` 各节点在操作前所属的线程（只有占位的
  连通块没有编号，不计入）；桥接邮件会让多个旧编号进入 `Gone`，更早邮件
  成为新根时旧根编号进入 `Gone`。
- 途径 2：涉及的线程 = 选中的候选线程；同 ts 且新 id 更小时换根，旧编号
  进入 `Gone`，否则为空。
- 途径 3：恒为空。

### 拒绝顺序（只报第一个，被拒不改任何状态）

1. 参数非法：构造参数越界（`W` 须在 `[0,1e9]`、`N` 在 `[1,1e5]`）、`id` 为空、
   `ts` 越界 `[0,1e12]`、`refs` 超 50 项、`refs`/`inReplyTo` 含空串、
   `refs`/`inReplyTo` 含 `id` 自身；
2. 重复登记（`id` 此前已是真实邮件）；
3. 容量不足（现有节点数 + `L` 与 `id` 中此前不存在的节点个数 > N；恰等通过）。

所有方法可用单把 `sync.RWMutex` 并发调用，结果等价于某个串行顺序；
`go test -race` 覆盖并发登记与查询。

### 本地验证

```bash
# 全量测试（含 2000 组随机序列对拍、-v 可看每步输入/输出/判定依据日志）
go test ./...
go test -race -v ./ontology

# 只跑对拍 / 候选计数器对照
go test ./ontology -run TestDifferentialAgainstNaive -v
go test ./ontology -run TestSubjectExamCounter -v

# 覆盖率与检查
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
gofmt -l .
go vet ./...
```

对拍说明：`ontology/naive_test.go` 是按同一规则逐步写成的朴素模拟（线程用
显式节点集合、合并逐元素搬移、候选线性扫描全部线程）；
`ontology/diff_test.go` 用 2000 组随机登记序列逐步比对 `Add` 结果、错误类别、
`Threads`/`ThreadOf`/`Members`，日志打印每步输入、输出与判定依据。

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
