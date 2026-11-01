// Package testenv 为集成测试读取环境变量。
//
// 优先读进程环境；未设置时回退读取仓库根目录的 .env 文件。
// 之所以放在代码里而不是 Makefile：.env 在 Windows 上通常是 CRLF，
// 用 make 的 include 会把 \r 带进 DSN；而测试可能被 PowerShell、Git Bash
// 或 CI 直接以 go test 调用，不经过 make。
package testenv

import (
	"os"
	"path/filepath"
	"strings"
)

// Lookup 返回环境变量 key 的值；进程环境没有时，从当前目录向上最多 6 层查找 .env。
func Lookup(key string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for depth := 0; depth < 6; depth++ {
		if data, readErr := os.ReadFile(filepath.Join(dir, ".env")); readErr == nil {
			value, ok := parse(string(data), key)
			if !ok {
				return ""
			}
			return value
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

func parse(content, key string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(name) != key {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		return value, true
	}
	return "", false
}
