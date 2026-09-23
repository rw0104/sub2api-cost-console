package routepool

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

const poolLockTimeout = 3 * time.Second

// ValidateStoragePath refuses symlinks/reparse points in every existing parent.
// The final component may not exist, but an existing one must be a regular file.
func ValidateStoragePath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return errors.New("代理池路径无效")
	}
	for current := abs; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return errors.New("无法检查代理池路径")
		}
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || isReparsePoint(current) {
				return errors.New("代理池路径不允许符号链接或目录联接")
			}
			if current == abs && !info.Mode().IsRegular() || current != abs && !info.IsDir() {
				return errors.New("代理池路径类型无效")
			}
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func lockPool(path string) (func(), error) {
	if err := ValidateStoragePath(path); err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("无法创建代理池目录")
	}
	if err := ValidateStoragePath(path); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, errors.New("无法保护代理池目录权限")
	}
	lockPath := path + ".lock"
	if err := ValidateStoragePath(lockPath); err != nil {
		return nil, err
	}
	f, err := openLockFile(lockPath)
	if err != nil {
		return nil, errors.New("无法打开代理池锁")
	}
	deadline := time.Now().Add(poolLockTimeout)
	for {
		locked, err := tryLock(f)
		if err != nil {
			_ = f.Close()
			return nil, errors.New("无法锁定代理池记录")
		}
		if locked {
			return func() { unlockFile(f); _ = f.Close() }, nil
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, errors.New("代理池记录正被其它进程占用，请稍后重试")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
