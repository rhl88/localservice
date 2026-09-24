// Package buildinfo 提供构建期注入的版本信息。
// 版本号由 build.ps1 通过 -ldflags "-X ...Version=x.y.z" 注入；
// 未注入时为 dev（如 go run 直接运行源码）。
package buildinfo

// Version 服务版本号，与版本服务器（Versionsvr 应用）发布的本地服务版本号对应。
var Version = "dev"
