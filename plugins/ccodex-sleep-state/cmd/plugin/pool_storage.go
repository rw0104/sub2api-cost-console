package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"local.sub2api/ccodex-sleep-state/internal/upstream/routepool"
)

var installedVersionDirectory = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?-[a-fA-F0-9]{12}-[0-9]+$`)

// Pool history belongs to the plugin ID, outside the replaceable version tree.
// A standalone/debug executable must use explicit in-memory test injection; it
// must never silently persist into a temporary directory or the process CWD.
func resolvePoolPath(executable string) (string, error) {
	invalidLayout := errors.New("代理池持久化需要通过宿主安装的插件运行；当前运行目录不是受支持的安装布局")
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable {
		return "", invalidLayout
	}
	runtimeDir := filepath.Dir(executable)
	runtimeKey := filepath.Base(runtimeDir)
	filename := "plugin"
	switch runtimeKey {
	case "windows-amd64":
		filename += ".exe"
	case "linux-amd64", "linux-arm64":
	default:
		return "", invalidLayout
	}
	runtimes := filepath.Dir(runtimeDir)
	versionDir := filepath.Dir(runtimes)
	idDir := filepath.Dir(versionDir)
	installedDir := filepath.Dir(idDir)
	if filepath.Base(executable) != filename || filepath.Base(runtimes) != "runtimes" ||
		filepath.Base(idDir) != pluginID || filepath.Base(installedDir) != "installed" ||
		!installedVersionDirectory.MatchString(filepath.Base(versionDir)) {
		return "", invalidLayout
	}
	if err := routepool.ValidateStoragePath(executable); err != nil {
		return "", err
	}
	manifestPath := filepath.Join(versionDir, "manifest.json")
	if err := routepool.ValidateStoragePath(manifestPath); err != nil {
		return "", err
	}
	file, err := os.Open(manifestPath)
	if err != nil && !os.IsNotExist(err) {
		return "", errors.New("无法验证插件安装清单")
	}
	if err == nil {
		var manifest struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(file, 2<<20)).Decode(&manifest)
		_ = file.Close()
		if decodeErr != nil || manifest.ID != pluginID || manifest.Version == "" ||
			!strings.HasPrefix(filepath.Base(versionDir), manifest.Version+"-") {
			return "", errors.New("插件安装清单与持久化目录不匹配")
		}
	}
	path := filepath.Join(idDir, ".state", "routepool.json")
	if err := routepool.ValidateStoragePath(path); err != nil {
		return "", err
	}
	return path, nil
}

func openInstalledPool() (*routepool.Store, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, errors.New("无法确定插件安装目录")
	}
	path, err := resolvePoolPath(executable)
	if err != nil {
		return nil, err
	}
	pool, err := routepool.Open(path)
	if err != nil {
		return nil, err
	}
	if _, err := pool.RecoverQualificationFailures(); err != nil {
		return nil, err
	}
	return pool, nil
}
