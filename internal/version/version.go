package version

// Version 由发布构建的 -ldflags 注入；源码构建明确标记为开发版。
var Version = "0.0.0-dev"
