//go:build tools

// Package tools 用于把 gomobile 相关依赖固定在 go.mod 中。
//
// gomobile bind 生成的绑定代码会依赖 golang.org/x/mobile/bind，且要求本模块显式
// require golang.org/x/mobile。但项目源码本身不直接 import 它，`go mod tidy` 会因此
// 把它移除。这里用 build tag "tools" 做一个仅编译期占位的 import，既不进入实际构建，
// 又能让依赖稳定保留在 go.mod / go.sum 中，保证 CI 里的 gomobile bind 可复现。
package tools

import (
	_ "golang.org/x/mobile/bind"
)
