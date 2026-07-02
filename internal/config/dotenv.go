package config

import (
	"path/filepath"

	"github.com/joho/godotenv"
)

// LoadDotEnv loads a .env from the CWD, then from the config file's directory,
// BEFORE ${ENV} interpolation. godotenv never overrides an already-set variable,
// so real secret injection always wins. Missing .env files are ignored.
func LoadDotEnv(cfgPath string) {
	_ = godotenv.Load()
	if dir := filepath.Dir(cfgPath); dir != "." && dir != "" {
		_ = godotenv.Load(filepath.Join(dir, ".env"))
	}
}
