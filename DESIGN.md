状态/事件模型：状态机逐字节吐出三类事件 Byte（词的一个字面字节）、Start、End。
words 层把 Start..End 之间（即使零个 Byte）收成一个词；事件流之外的空白不产生词。

第2条推导（双引号内反斜杠）：POSIX 规定双引号内反斜杠仅在后随 $、`、"、\、<换行>
时保留转义意义——前四个场景反斜杠与后随字符合为一个字面对象（\X→X），<换行>场景
二者一并删除（续行）；其余后随字符反斜杠不获特殊意义，故 \ 与 X 作为两个字面字节
保留（"a\b"→a\b）。判定必须延迟到看到“后随字节”才能做出，因此设 B2（双引号待定）
状态：看到那五类则消费该字节（LF 不发字节，其余发后随字节），否则先发一个 '\' 再
回退按 Q2 规则处理该字节。

第4条推导（空词）：词的边界是“词开始/词结束”事件而非字节。无引号部分开始或任一
引号开启都使词开始，引号关闭后词仍存活（允许后续 a""b 拼接），直到空白或 EOF 才
结束。于是：""'' → 1 个空词（两次开/闭引号都在同一个词内，零字面字节）；a"" →
1 个词 "a"；裸的 \<换行> 且前后无内容 → 0 个词，因为续行在词开始之前就被整体删除，
没有任何部件开启过词；连续空白同理不 Start，故不产生空词。

状态转移表（B0/B2 为反斜杠待定状态；emit=发 Byte；‘续行’=不发字节）
| 状态 | 空格/制表 | 换行 | ' | " | \ | 其他/$/` |
| O 词外 | O 无事件 | O | Q1,Start | Q2,Start | B0,Start | O',Start emit |
| O' 词中 | O,End | O,End | Q1 | Q2 | B0 | O' emit |
| Q1 单引 | Q1 emit | Q1 emit | O' | Q1 emit | Q1 emit | Q1 emit |
| Q2 双引 | Q2 emit | Q2 emit | Q2 emit | O' | B2 | Q2 emit |
| B0 无引待定 | O' emit | 回 O*（续行；词外则未 Start） | O' emit ' | O' emit " | O' emit \ | O' emit |
| B2 双引待定 | 发 '\' 再按 Q2 处理空格 | 回 Q2（续行） | 发 '\' 再 Q2 处理 ' | O' emit " | O' emit \ | Q2 emit（$ ` 同此） |
注：空白仅 ASCII SPC/TAB 及换行（引号外换行即词分隔符）；O* 按词是否存活取 O/O'。

EOF：Q1→ErrUnclosedSingle（偏移=开引号位置）；Q2/B2→ErrUnclosedDouble（开引号位置）；
B0→ErrTrailingBackslash（该 '\' 位置）；词存活→先发 End。

Quote：每个词整体用单引号包裹，词内每个 ' 替换为 '\''（关单引号、转义的单引号、
重开单引号）。单引号内反斜杠无转义能力，无法在单引号内直接表达 '，只能出引号后用
\' 表达再回去；空词得 ''。该写法对任意字节安全，保证 Split(Quote(w))==w。

测试对照表（语义 → 测试函数）
1 单引号字面量（含 \）：lex.TestEvents（single-* 两行）、words.TestSplit 前两行
2 双引号五种转义/保留+续行：lex.TestEvents（double-*）、words.TestSplit（double-*）
3 无引号转义与续行：lex.TestEvents（unquoted-*）、words.TestSplit（unquoted-*）
4 拼接与空词：lex.TestEmptyWordEvents、words.TestSplit（concat/empty-*）
5 三类可区分错误与偏移：lex.TestEndErrors、words.TestSplitErrors
6 任意切分点一致：words.TestAllSplitPoints（每个切分点一刀，含 \ 与后续字节之间）
7 Quote 往返：words.TestQuoteRoundtrip（固定表 + 500 组随机，含空词/引号/\/$/换行/非 ASCII）
计数器约束：lex.TestProcessed、words.TestStreamCounter（1MB 逐字节，断言计数==字节数）
