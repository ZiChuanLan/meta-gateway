package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds the application configuration parsed from environment variables.
type Config struct {
	HTTPAddr   string
	DataDir    string
	AdminToken string
	MasterKey  string
	RetryTimes int
	Cooldown   time.Duration
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		HTTPAddr:   envStr("HTTP_ADDR", ":4100"),
		DataDir:    envStr("DATA_DIR", "./data"),
		AdminToken: envStr("ADMIN_TOKEN", ""),
		MasterKey:  envStr("MASTER_KEY", ""),
		RetryTimes: envInt("RETRY_TIMES", 2),
		Cooldown:   time.Duration(envInt("COOLDOWN_SECONDS", 30)) * time.Second,
	}
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
