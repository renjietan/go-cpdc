// config/config.go
package config

import (
	"os"
	"strconv"
	"sync"
)

// Env 存放程序运行时的所有环境参数
type Env struct {
	LogLevel   string
	ConfigPath string
	Interface  string
}

var (
	once   sync.Once
	global *Env
)

// Init 只在 main 中调用一次（幂等，可被 GetEnv 隐式调用）
func Init(configPath, logLevel, iFace *string) *Env {
	once.Do(func() {
		global = &Env{
			Interface:  *iFace,
			LogLevel:   *logLevel,
			ConfigPath: *configPath,
		}
	})

	return global
}

// GetEnv 任意地方调用，返回全局唯一实例
func GetEnv() *Env {
	return global
}

func getString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
