package config

import (
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// LoadDotEnv 加载本地环境变量文件（不进 git）。
// 从当前工作目录向上查找含 go.mod 的模块根，再加载该根下的 configs/.env 与 .env，
// 这样即使从子目录执行 `go run ./cmd/importer` 也能读到仓库根目录的 .env。
// 先 Load configs/.env（不覆盖已在 shell 里 export 的变量），再 Overload 根目录 .env。
func LoadDotEnv() {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	root := findGoModuleRoot(wd)
	_ = godotenv.Load(filepath.Join(root, "configs", ".env"))
	_ = godotenv.Overload(filepath.Join(root, ".env"))
}

func findGoModuleRoot(start string) string {
	dir := start
	for range 12 {
		if dir == "" {
			break
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return start
}
