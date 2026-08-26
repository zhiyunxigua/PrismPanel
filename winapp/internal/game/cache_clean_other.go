//go:build !windows

package game

// mcAccountCacheLocation 非 Windows：国际版账号保存在 mc-account.json 文件。
func mcAccountCacheLocation(entry *CacheEntry) {
	path, err := mcStorePath()
	if err != nil {
		entry.Description = "无法定位账号文件：" + err.Error()
		return
	}
	entry.Path = path
	entry.Exists, entry.SizeBytes = pathExistsSize(path)
}
