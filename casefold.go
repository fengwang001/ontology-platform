package ontology

// fullFold 收录 Unicode CaseFolding 中常见的多字符全折叠（F 类）映射。
// 单字符折叠由 unicode.ToLower 覆盖（含 Kelvin 记号 U+212A→k、
// 希腊词尾小写 ς→σ 等），此处只列 ToLower 无法表达的多字符情形。
var fullFold = map[rune]string{
	'ß': "ss",          // U+00DF 德语 sharp s
	'İ': "i̇", // U+0130 土耳其语点大写 I
	'ŉ': "ʼn", // U+0149
	'ǰ': "ǰ", // U+01F0
	'ΐ': "ΐ", // U+0390
	'ΰ': "ΰ", // U+03B0
	'և': "եւ", // U+0587 亚美尼亚语连字
	'ẖ': "ẖ", // U+1E96
	'ẗ': "ẗ", // U+1E97
	'ẘ': "ẘ", // U+1E98
	'ẙ': "ẙ", // U+1E99
	'ẚ': "aʾ", // U+1E9A
	'ὐ': "ὐ", // U+1F50
	'ὒ': "ὒ", // U+1F52
	'ὔ': "ὔ", // U+1F54
	'ὖ': "ὖ", // U+1F56
	'ﬀ': "ff",           // U+FB00 拉丁连字
	'ﬁ': "fi",           // U+FB01
	'ﬂ': "fl",           // U+FB02
	'ﬃ': "ffi",          // U+FB03
	'ﬄ': "ffl",          // U+FB04
	'ﬅ': "st",           // U+FB05
	'ﬆ': "st",           // U+FB06
}
