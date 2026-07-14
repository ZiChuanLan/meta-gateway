package config

import "path/filepath"

type RestoreConfig struct{ DataDir, BackupDir string }

func LoadRestore() RestoreConfig {
	dataDir := envStr("DATA_DIR", "./data")
	return RestoreConfig{DataDir: dataDir, BackupDir: envStr("BACKUP_DIR", filepath.Join(dataDir, "backups"))}
}
