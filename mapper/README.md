# mapper — 跨平台文件名安全映射器

`package mapper` 把源端目录树中的文件名映射为目标端（不区分大小写、禁用部分
字符与设备保留名、限制名字与路径字节数）可用的名字。同一目录内名字唯一，
已分配的名字不随后续增删漂移，相同调用序列重放得到完全相同的结果。

## 构造参数

`mapper.New(Config{MaxBytes, MaxPath, MaxEntries})`：

- `MaxBytes`：单个映射名的字节上限，范围 `16..255`。
- `MaxPath`：映射后整条路径的字节上限，范围 `MaxBytes..4096`。
- `MaxEntries`：每个目录的直接子条目数上限，范围 `1..100000`。

参数越界时 `New` 直接 panic（属于编程错误而非可恢复的调用错误）。

条理由整数 id 标识，根目录 id 为 `0`；条目分文件与目录，只有目录可作父。
源名 `src` 必须非空、不含 `/` 与 NUL、为合法 UTF-8，否则返回
`ErrInvalidName`。

条目的路径长度 = 全部祖先与自身映射名的字节数之和 + 段间 `/` 的个数
（根下条目就是其映射名长度；根自身路径为空串、长度 0）。

## 基础映射（四步，依次执行）

1. **转义**：逐个 rune，若属于 `< > : " \ | ? *` 这八个字符、码点小于
   `0x20`，或本身是 `%`，替换为 `%` 加该字节的两位大写十六进制（这些码点
   均为单字节）；其余 rune 原样保留。
2. **结尾处理**：若结果以 `.` 或空格结尾，把最后一个字符替换为 `%2E` 或
   `%20`。
3. **保留名**：取第一个 `.` 之前的部分为主干（无 `.` 取全部）；主干按
   ASCII 大写折叠（A-Z → a-z）后属于 `CON PRN AUX NUL`、`COM1..COM9`、
   `LPT1..LPT9` 之一时，把整个名字的第一个字符替换为其 `%XX` 转义（保留
   原大小写，如 `Aux.txt` → `%41ux.txt`）。
4. **超长截断**：字节数大于 `MaxBytes` 时，对第三步结果的全部字节计算
   32 位 FNV-1a（offset 2166136261，prime 16777619），格式化为 8 位大写
   十六进制。把名字切成记号（一个 rune，或一个 `%XX` 转义），取最长的
   记号前缀 P 使 `len(P)+9 ≤ MaxBytes`，结果为 `P + "~" + h`。绝不会切
   断多字节 rune 或半个 `%XX`。

例子：

| 源名 | 映射名 | 依据 |
| --- | --- | --- |
| `a:b?.` | `a%3Ab%3F%2E` | 第 1 步转义 `:` `?`，第 2 步转义结尾 `.` |
| `CON` | `%43ON` | 第 3 步保留名，转义首字符 `C` |
| `NUL.` | `NUL%2E` | 第 2 步先生成 `%2E`，主干 `NUL%2E` 不是保留名 |
| `Aux.txt` | `%41ux.txt` | 保留名 `aux`，转义首字符 `A` |

转义是单射的：原始 `%` 会变成 `%25`，所以输出中的 `%XX` 不可能与字面
百分号混淆。

## 目录内唯一性与稳定性

以映射名的 ASCII 折叠结果为键。`Add` 先算基础名 `t0`：

- 键未占用：直接分配 `t0`。
- 已占用：对 `n = 2,3,…` 尝试 `base + "~" + n + ext`，其中以最后一个
  下标 **大于 0** 的 `.` 将 `t0` 拆成 `base` 与 `ext`（`.foo` 无 ext）。
  候选超过 `MaxBytes` 时，从 `base` 末尾按记号逐个缩短到刚好放下；`base`
  缩到空仍放不下则 `ErrCannotFit`。跳过折叠键已被占用的编号（包括被真实
  条目占用的键，如已有 `a~2` 时新冲突直接取 `~3`）。

已分配的名字仅在所属条目被 `Remove` 或 `Rename` 时释放，其他条目的名字
绝不漂移；释放出的槽位会被后续分配复用（删除 `Readme.md` 后新增
`ReadMe.md` 得到 `ReadMe.md`，`~2/~3` 编号保持不变）。

## Remove / Rename

- `Remove(parent, src)`：不存在 `ErrNotFound`；目录非空 `ErrNotEmpty`。
- `Rename(parent, src, newSrc)`：条目 id 不变；`newSrc == src` 为成功的空
  操作。语义是**先释放 `src` 的名字、再按 Add 规则为 `newSrc` 分配**
  （因此把 `Readme.md` 改名为 `readme.md` 得到 `readme.md` 而非
  `readme~2.md`）。任何一步失败都恢复为调用前状态。
- **路径长度判定**：`Add` 检查新条目自身；目录 `Rename` 检查其整棵子树
  （改名后所有后代的路径同步变化）。超限返回 `ErrPathTooLong` 并回滚，
  子树名字与 id 保持原样。

## 只读查询

- `Lookup(parent, src)`：源名大小写敏感地查找 id。
- `Path(id)`：映射后的绝对路径（段以 `/` 连接，根为 `""`）。
- `Names(parent)`：该目录下的映射名，按字节序排序。

## 错误优先级

拒绝的调用不改变任何状态，只报固定优先级中的第一个错误：

- `Add`：`ErrInvalidName` > `ErrNoParent`（父不存在或不是目录）>
  `ErrExists`（同目录下 `src` 大小写敏感地已存在）> `ErrFull` >
  `ErrCannotFit` > `ErrPathTooLong`。
- `Rename`：`newSrc` 的 `ErrInvalidName` > `src` 的 `ErrNotFound` >
  同名空操作 > `ErrExists` > `ErrCannotFit` > `ErrPathTooLong`
  （改名不检查 `ErrFull`）。

## 并发

所有方法可被并发调用，内部以读写锁串行化，结果等价于某个串行顺序。
任何时刻同一目录内折叠键两两不同，名字不超过 `MaxBytes`、路径不超过
`MaxPath`，且不含目标端禁用字符；重放相同调用序列得到完全相同的映射。

## 本地验证

```bash
# 若 go 不在 PATH
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache

go test ./...
go test -race -v ./mapper/
go vet ./...
gofmt -l .
```

随机差分测试（`TestRandomDifferential`）共 2000 组随机树操作，在生产实现
与一份独立、逐步对照规则写成的朴素实现（`naive_test.go`）之间比较每个操作
的错误、每个 id 的路径与每个目录的名字排序；输入、输出与判定依据写入
`mapper/diff_run.log`。
