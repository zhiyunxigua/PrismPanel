//go:build windows

package game

// mcAccountCacheLocation 国际版账号位于 Windows 凭据管理器（不占用文件路径）。
func mcAccountCacheLocation(entry *CacheEntry) {
	entry.Path = "Windows 凭据管理器（" + mcLocalAccountTarget + "）"
	if _, err := NewMCLocalStore().Load(); err == nil {
		entry.Exists = true
		entry.SizeText = "1 个账号"
	}
}
