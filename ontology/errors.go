package ontology

import "errors"

// 参数非法（构造参数越界、id 空、ts 越界、refs 超 50、引用含空串或自引用）。
var ErrInvalidArgument = errors.New("ontology: invalid argument")

// 重复登记：id 此前已是真实邮件。
var ErrDuplicate = errors.New("ontology: duplicate message id")

// 容量不足：节点数会超过上限 N。
var ErrCapacity = errors.New("ontology: node capacity exceeded")

// 查询对象（节点或线程）不存在。
var ErrNotFound = errors.New("ontology: not found")
