package game

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

const authlibInjectorArtifactURL = "https://authlib-injector.yushi.moe/artifacts/authlib-injector-1.2.5.jar"

// mcAuthlibInjectorJar 返回 authlib-injector 的本地 jar 路径（位于存储根目录）。
func mcAuthlibInjectorJar() (string, error) {
	root, err := mcStoreRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "authlib-injector.jar"), nil
}

// EnsureMCAuthlibInjector 确保 authlib-injector jar 已下载，返回其路径。
// 仅用于第三方认证服务器启动时（参照 PCL2 首次使用自动下载）。
func EnsureMCAuthlibInjector(ctx context.Context) (string, error) {
	jar, err := mcAuthlibInjectorJar()
	if err != nil {
		return "", err
	}
	if fileExists(jar) {
		return jar, nil
	}
	// authlib-injector 无国内镜像，直接官方源下载（不放行镜像改写以免路径被改写错）
	if err := downloadURLOnce(ctx, authlibInjectorArtifactURL, jar); err != nil {
		return "", fmt.Errorf("authlib-injector 下载失败：%w", err)
	}
	return jar, nil
}

// mcAuthlibInjectorAgent 返回 -javaagent: 参数（供 BuildMCLaunchArgs 使用）。
func mcAuthlibInjectorAgent(account MCAccount) (string, error) {
	jar, err := mcAuthlibInjectorJar()
	if err != nil {
		return "", err
	}
	if !fileExists(jar) {
		return "", fmt.Errorf("authlib-injector 尚未就绪：%s", jar)
	}
	return "-javaagent:" + jar + "=" + strings.TrimRight(account.AuthServer, "/"), nil
}

// mcUserType 辅助：第三方账号默认视为 legacy（authlib-injector 会接管鉴权）。
func isMCAuthlibThirdParty(account MCAccount) bool {
	return account.Mode == MCAuthThirdParty
}
