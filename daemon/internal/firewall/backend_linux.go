//go:build linux

package firewall

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"time"
)

type linuxBackend struct {
	binary string
}

func newPlatformBackend() backend {
	return &linuxBackend{}
}

func (b *linuxBackend) Status(_ context.Context) backendStatus {
	if b.binary == "" {
		path, err := exec.LookPath("nft")
		if err != nil {
			return backendStatus{Reason: "未找到 nft 命令，请安装 nftables", Name: "nftables"}
		}
		b.binary = path
	}
	if os.Geteuid() != 0 {
		return backendStatus{Reason: "守护进程必须以 root 身份运行才能管理 nftables", Name: "nftables"}
	}
	return backendStatus{Supported: true, Name: "nftables"}
}

func (b *linuxBackend) Inspect(ctx context.Context) (string, bool, error) {
	tables, err := b.run(ctx, "list", "tables")
	if err != nil {
		return "", false, err
	}
	if !containsFirewallTable(tables) {
		return "", false, nil
	}
	contents, err := b.run(ctx, "list", "table", "inet", firewallTableName)
	if err != nil {
		return "", false, err
	}
	return contents, true, nil
}

func (b *linuxBackend) Apply(ctx context.Context, script string) error {
	if strings.TrimSpace(script) == "" {
		return nil
	}
	if _, err := b.runFile(ctx, []string{"--check", "--file", "-"}, script); err != nil {
		return fmt.Errorf("检查 nftables 规则: %w", err)
	}
	if _, err := b.runFile(ctx, []string{"--file", "-"}, script); err != nil {
		return fmt.Errorf("应用 nftables 规则: %w", err)
	}
	return nil
}

func (b *linuxBackend) AddGrant(ctx context.Context, address netip.Addr, ttl time.Duration) error {
	setName, err := grantSetName(address)
	if err != nil {
		return err
	}
	script := fmt.Sprintf("add element inet %s %s { %s timeout %s }\n", firewallTableName, setName, address.Unmap(), formatDuration(ttl))
	if err := b.addGrantFresh(ctx, script); err == nil {
		return nil
	} else if !isExistingElementError(err) {
		return err
	}
	return b.resetGrant(ctx, setName, address, script)
}

func (b *linuxBackend) addGrantFresh(ctx context.Context, script string) error {
	if _, err := b.runFile(ctx, []string{"--check", "--file", "-"}, script); err != nil {
		return fmt.Errorf("检查临时访问授权: %w", err)
	}
	if _, err := b.runFile(ctx, []string{"--file", "-"}, script); err != nil {
		return fmt.Errorf("添加临时访问授权: %w", err)
	}
	return nil
}

func (b *linuxBackend) resetGrant(ctx context.Context, setName string, address netip.Addr, addScript string) error {
	deleteScript := fmt.Sprintf("delete element inet %s %s { %s }\n", firewallTableName, setName, address.Unmap())
	refreshScript := deleteScript + addScript
	if _, err := b.runFile(ctx, []string{"--check", "--file", "-"}, refreshScript); err != nil {
		if isMissingElementError(err) {
			return b.addGrantFresh(ctx, addScript)
		}
		return fmt.Errorf("检查临时访问授权刷新: %w", err)
	}
	if _, err := b.runFile(ctx, []string{"--file", "-"}, refreshScript); err != nil {
		if isMissingElementError(err) {
			return b.addGrantFresh(ctx, addScript)
		}
		return fmt.Errorf("删除旧临时访问授权: %w", err)
	}
	return nil
}

func (b *linuxBackend) RemoveGrant(ctx context.Context, address netip.Addr) error {
	setName, err := grantSetName(address)
	if err != nil {
		return err
	}
	script := fmt.Sprintf("delete element inet %s %s { %s }\n", firewallTableName, setName, address.Unmap())
	if _, err := b.runFile(ctx, []string{"--check", "--file", "-"}, script); err != nil {
		if isMissingElementError(err) {
			return nil
		}
		return fmt.Errorf("检查临时访问授权撤销: %w", err)
	}
	if _, err := b.runFile(ctx, []string{"--file", "-"}, script); err != nil {
		if isMissingElementError(err) {
			return nil
		}
		return fmt.Errorf("撤销临时访问授权: %w", err)
	}
	return nil
}

func (b *linuxBackend) run(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, b.binary, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", commandError(err, output)
	}
	return string(output), nil
}

func (b *linuxBackend) runFile(ctx context.Context, args []string, script string) (string, error) {
	command := exec.CommandContext(ctx, b.binary, args...)
	command.Stdin = strings.NewReader(script)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", commandError(err, output)
	}
	return string(output), nil
}

func containsFirewallTable(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "table" && fields[1] == "inet" && fields[2] == firewallTableName {
			return true
		}
	}
	return false
}

func grantSetName(address netip.Addr) (string, error) {
	address = address.Unmap()
	if !address.IsValid() {
		return "", fmt.Errorf("临时授权地址无效")
	}
	if address.Is4() {
		return "prismpanel_direct_grants4", nil
	}
	return "prismpanel_direct_grants6", nil
}

func commandError(err error, output []byte) error {
	message := strings.TrimSpace(string(bytes.TrimSpace(output)))
	if message == "" {
		return err
	}
	if len(message) > 2048 {
		message = message[:2048]
	}
	return fmt.Errorf("%w: %s", err, message)
}

func isMissingElementError(err error) bool {
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "no such file") || strings.Contains(value, "no such element") || strings.Contains(value, "not found")
}

func isExistingElementError(err error) bool {
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "file exists") || strings.Contains(value, "already exists")
}
