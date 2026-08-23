package firewall

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/atomicfile"
)

const (
	stateFileName      = "firewall.json"
	maxTemporaryGrants = 2048
)

type persistedState struct {
	SchemaVersion int          `json:"schema_version"`
	Revision      int64        `json:"revision"`
	Rules         []Rule       `json:"rules"`
	System        SystemAccess `json:"system"`
	AppliedHash   string       `json:"applied_hash,omitempty"`
	AppliedAt     *time.Time   `json:"applied_at,omitempty"`
}

type Service struct {
	mu        sync.Mutex
	backend   backend
	logger    *slog.Logger
	statePath string
	port      int

	state        persistedState
	grants       map[string]Grant
	backendState backendStatus
	observedHash string
	tablePresent bool
	drift        bool
	lastError    string
}

func New(dataDir string, daemonPort int, logger *slog.Logger) (*Service, error) {
	return newService(filepath.Join(dataDir, stateFileName), daemonPort, newPlatformBackend(), logger)
}

func newService(statePath string, daemonPort int, platform backend, logger *slog.Logger) (*Service, error) {
	if daemonPort < 1 || daemonPort > 65535 {
		return nil, fmt.Errorf("daemon firewall port is invalid")
	}
	if logger == nil {
		logger = slog.Default()
	}
	service := &Service{
		backend: platform, logger: logger, statePath: statePath, port: daemonPort,
		grants: make(map[string]Grant),
		state: persistedState{
			SchemaVersion: SchemaVersion,
			Rules:         []Rule{},
			System:        SystemAccess{ControlSources: []string{}, GrantTTLSeconds: int(DefaultGrantTTL.Seconds())},
		},
	}
	if err := service.load(); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *Service) load() error {
	contents, err := os.ReadFile(s.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read firewall state: %w", err)
	}
	var state persistedState
	if err := json.Unmarshal(contents, &state); err != nil {
		return fmt.Errorf("decode firewall state: %w", err)
	}
	if state.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported firewall state schema %d", state.SchemaVersion)
	}
	if state.Rules == nil {
		state.Rules = []Rule{}
	}
	if state.System.GrantTTLSeconds == 0 {
		state.System.GrantTTLSeconds = int(DefaultGrantTTL.Seconds())
	}
	normalizedControls, err := normalizeSources(state.System.ControlSources, false)
	if err != nil {
		return fmt.Errorf("normalize firewall control sources: %w", err)
	}
	state.System.ControlSources = normalizedControls
	for index, rule := range state.Rules {
		normalized, err := s.normalizeRule(rule.ID, RuleInput{
			Enabled: rule.Enabled, Protocols: rule.Protocols, Ports: rule.Ports, Sources: rule.Sources, Note: rule.Note,
		})
		if err != nil {
			return fmt.Errorf("normalize firewall rule %q: %w", rule.ID, err)
		}
		normalized.CreatedAt = rule.CreatedAt
		normalized.UpdatedAt = rule.UpdatedAt
		state.Rules[index] = normalized
	}
	if err := s.validateState(state); err != nil {
		return fmt.Errorf("validate firewall state: %w", err)
	}
	s.state = state
	return nil
}

// Initialize restores a previously applied local state only when its nftables table is absent.
func (s *Service) Initialize(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.refreshLocked(ctx)
	if !status.Supported || s.lastError != "" {
		return nil
	}
	if s.activeStateLocked() && !s.tablePresent {
		if err := s.applyCandidateLocked(ctx, cloneState(s.state)); err != nil {
			s.lastError = err.Error()
			return err
		}
	}
	return nil
}

func (s *Service) Status(ctx context.Context) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked(ctx)
	return s.statusLocked()
}

func (s *Service) View(ctx context.Context) View {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked(ctx)
	s.expireGrantsLocked(time.Now().UTC())
	return View{
		Status: s.statusLocked(),
		Rules:  cloneRules(s.state.Rules),
		Grants: sortedGrants(s.grants),
	}
}

func (s *Service) CreateRule(ctx context.Context, input CreateRuleInput) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepareMutationLocked(ctx, input.ExpectedRevision); err != nil {
		return View{}, err
	}
	id, err := randomID()
	if err != nil {
		return View{}, apperr.Wrap("INTERNAL", "无法生成防火墙规则 ID", err)
	}
	rule, err := s.normalizeRule(id, input.Rule)
	if err != nil {
		return View{}, err
	}
	now := time.Now().UTC()
	rule.CreatedAt, rule.UpdatedAt = now, now
	candidate := cloneState(s.state)
	candidate.Revision++
	candidate.Rules = append(candidate.Rules, rule)
	if err := s.applyCandidateLocked(ctx, candidate); err != nil {
		return View{}, err
	}
	return s.viewLocked(), nil
}

func (s *Service) UpdateRule(ctx context.Context, ruleID string, input UpdateRuleInput) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepareMutationLocked(ctx, input.ExpectedRevision); err != nil {
		return View{}, err
	}
	candidate := cloneState(s.state)
	index := findRule(candidate.Rules, ruleID)
	if index < 0 {
		return View{}, apperr.New("FIREWALL_RULE_NOT_FOUND", "防火墙规则不存在")
	}
	rule, err := s.normalizeRule(ruleID, input.Rule)
	if err != nil {
		return View{}, err
	}
	rule.CreatedAt = candidate.Rules[index].CreatedAt
	rule.UpdatedAt = time.Now().UTC()
	candidate.Rules[index] = rule
	candidate.Revision++
	if err := s.applyCandidateLocked(ctx, candidate); err != nil {
		return View{}, err
	}
	return s.viewLocked(), nil
}

func (s *Service) DeleteRule(ctx context.Context, ruleID string, input DeleteRuleInput) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepareMutationLocked(ctx, input.ExpectedRevision); err != nil {
		return View{}, err
	}
	candidate := cloneState(s.state)
	index := findRule(candidate.Rules, ruleID)
	if index < 0 {
		return View{}, apperr.New("FIREWALL_RULE_NOT_FOUND", "防火墙规则不存在")
	}
	candidate.Rules = append(candidate.Rules[:index], candidate.Rules[index+1:]...)
	candidate.Revision++
	if err := s.applyCandidateLocked(ctx, candidate); err != nil {
		return View{}, err
	}
	return s.viewLocked(), nil
}

func (s *Service) ConfigureSystem(ctx context.Context, callerSource string, input ConfigureSystemInput) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepareMutationLocked(ctx, input.ExpectedRevision); err != nil {
		return View{}, err
	}
	controls := append([]string(nil), input.System.ControlSources...)
	if input.System.IncludeCallerSource && strings.TrimSpace(callerSource) != "" {
		controls = append(controls, callerSource)
	}
	normalizedControls, err := normalizeSources(controls, false)
	if err != nil {
		return View{}, err
	}
	ttl := input.System.GrantTTLSeconds
	if ttl == 0 {
		ttl = int(DefaultGrantTTL.Seconds())
	}
	if ttl < 60 || ttl > 3600 {
		return View{}, apperr.New("INVALID_FIREWALL_SYSTEM", "临时访问有效期必须在 60 到 3600 秒之间")
	}
	if input.System.Enabled && len(normalizedControls) == 0 {
		return View{}, apperr.New("FIREWALL_LOCKOUT_RISK", "启用系统访问保护前必须保留至少一个 Panel 控制来源")
	}
	candidate := cloneState(s.state)
	candidate.System = SystemAccess{
		Enabled: input.System.Enabled, ControlSources: normalizedControls, GrantTTLSeconds: ttl,
	}
	candidate.Revision++
	if err := s.applyCandidateLocked(ctx, candidate); err != nil {
		return View{}, err
	}
	if !candidate.System.Enabled {
		s.grants = make(map[string]Grant)
	}
	return s.viewLocked(), nil
}

// GrantDirectAccess creates or refreshes a short-lived source IP grant for a direct ticket.
func (s *Service) GrantDirectAccess(ctx context.Context, source, sessionID, ticketID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.state.System.Enabled {
		return nil
	}
	if err := s.prepareBackendLocked(ctx); err != nil {
		return err
	}
	s.refreshObservedLocked(ctx)
	if s.lastError != "" {
		return apperr.Wrap("FIREWALL_INSPECT_FAILED", "无法确认当前防火墙状态", errors.New(s.lastError))
	}
	if s.drift {
		return apperr.New("FIREWALL_DRIFT", "PrismPanel 防火墙表已被外部修改，请先处理配置漂移")
	}
	address, err := netip.ParseAddr(strings.TrimSpace(source))
	if err != nil || address.IsUnspecified() {
		return apperr.New("INVALID_SOURCE_IP", "临时访问来源 IP 无效")
	}
	address = address.Unmap()
	if address.IsLoopback() {
		return nil
	}
	now := time.Now().UTC()
	s.expireGrantsLocked(now)
	key := grantKey(address.String(), sessionID, ticketID)
	if _, exists := s.grants[key]; !exists && len(s.grants) >= maxTemporaryGrants {
		return apperr.New("TOO_MANY_REQUESTS", "临时访问授权数量已达到节点上限")
	}
	ttl := time.Duration(s.state.System.GrantTTLSeconds) * time.Second
	expiresAt := now.Add(ttl)
	if err := s.backend.AddGrant(ctx, address, ttl); err != nil {
		s.lastError = err.Error()
		return apperr.Wrap("FIREWALL_APPLY_FAILED", "无法添加临时访问授权", err)
	}
	s.grants[key] = Grant{Source: address.String(), SessionID: sessionID, TicketID: ticketID, ExpiresAt: expiresAt}
	return nil
}

func (s *Service) RevokeSessionGrants(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	s.expireGrantsLocked(now)
	candidate := make(map[string]Grant, len(s.grants))
	for key, grant := range s.grants {
		candidate[key] = grant
	}
	removedSources := make(map[string]netip.Addr)
	for key, grant := range candidate {
		if grant.SessionID != sessionID {
			continue
		}
		address, err := netip.ParseAddr(grant.Source)
		if err == nil {
			removedSources[grant.Source] = address
		}
		delete(candidate, key)
	}
	if !s.state.System.Enabled {
		s.grants = candidate
		return nil
	}
	if err := s.prepareBackendLocked(ctx); err != nil {
		return err
	}
	s.refreshObservedLocked(ctx)
	if s.lastError != "" {
		return apperr.Wrap("FIREWALL_INSPECT_FAILED", "无法确认当前防火墙状态", errors.New(s.lastError))
	}
	if s.drift {
		return apperr.New("FIREWALL_DRIFT", "PrismPanel 防火墙表已被外部修改，请先处理配置漂移")
	}
	for source, address := range removedSources {
		if hasAnyGrantForSource(candidate, source) {
			continue
		}
		if err := s.backend.RemoveGrant(ctx, address); err != nil {
			s.lastError = err.Error()
			return apperr.Wrap("FIREWALL_APPLY_FAILED", "无法撤销临时访问授权", err)
		}
	}
	s.grants = candidate
	return nil
}

func (s *Service) prepareMutationLocked(ctx context.Context, expectedRevision int64) error {
	if err := s.prepareBackendLocked(ctx); err != nil {
		return err
	}
	s.refreshObservedLocked(ctx)
	if s.drift {
		return apperr.New("FIREWALL_DRIFT", "PrismPanel 防火墙表已被外部修改，请先处理配置漂移")
	}
	if expectedRevision != s.state.Revision {
		return apperr.New("FIREWALL_REVISION_CONFLICT", "防火墙规则已被其他操作更新，请刷新后重试")
	}
	return nil
}

func (s *Service) prepareBackendLocked(ctx context.Context) error {
	status := s.backend.Status(ctx)
	s.backendState = status
	if !status.Supported {
		return apperr.New("FIREWALL_UNSUPPORTED", status.Reason)
	}
	return nil
}

func (s *Service) refreshLocked(ctx context.Context) backendStatus {
	status := s.backend.Status(ctx)
	s.backendState = status
	if !status.Supported {
		s.tablePresent = false
		s.drift = false
		return status
	}
	s.refreshObservedLocked(ctx)
	return status
}

func (s *Service) refreshObservedLocked(ctx context.Context) {
	contents, exists, err := s.backend.Inspect(ctx)
	if err != nil {
		s.lastError = err.Error()
		return
	}
	s.lastError = ""
	s.tablePresent = exists
	if !exists {
		s.observedHash = ""
		s.drift = s.activeStateLocked()
		return
	}
	s.observedHash = hashContents(normalizeObservedTable(contents))
	s.drift = s.state.AppliedHash == "" || s.state.AppliedHash != s.observedHash
}

func (s *Service) applyCandidateLocked(ctx context.Context, candidate persistedState) error {
	if err := s.validateState(candidate); err != nil {
		return err
	}
	script, err := renderScript(candidate, s.grants, s.port, s.tablePresent)
	if err != nil {
		return apperr.Wrap("INVALID_FIREWALL_RULE", "无法生成防火墙规则", err)
	}
	if err := s.backend.Apply(ctx, script); err != nil {
		s.lastError = err.Error()
		return apperr.Wrap("FIREWALL_APPLY_FAILED", "nftables 原子应用失败", err)
	}
	contents, exists, err := s.backend.Inspect(ctx)
	if err != nil {
		s.lastError = err.Error()
		return apperr.Wrap("FIREWALL_INSPECT_FAILED", "防火墙规则已应用，但无法读取实际状态", err)
	}
	if exists {
		candidate.AppliedHash = hashContents(normalizeObservedTable(contents))
	} else {
		candidate.AppliedHash = ""
	}
	now := time.Now().UTC()
	candidate.AppliedAt = &now
	if err := persistState(s.statePath, candidate); err != nil {
		s.lastError = err.Error()
		return apperr.Wrap("CONFIG_WRITE_FAILED", "防火墙规则已应用，但无法保存 daemon 本地状态", err)
	}
	s.state = candidate
	s.tablePresent = exists
	s.observedHash = candidate.AppliedHash
	s.drift = false
	s.lastError = ""
	return nil
}

func (s *Service) validateState(state persistedState) error {
	if state.SchemaVersion != SchemaVersion || state.Revision < 0 {
		return apperr.New("INVALID_FIREWALL_STATE", "防火墙本地状态无效")
	}
	if state.System.GrantTTLSeconds < 60 || state.System.GrantTTLSeconds > 3600 {
		return apperr.New("INVALID_FIREWALL_SYSTEM", "临时访问有效期必须在 60 到 3600 秒之间")
	}
	controls, err := normalizeSources(state.System.ControlSources, false)
	if err != nil {
		return err
	}
	if state.System.Enabled && len(controls) == 0 {
		return apperr.New("FIREWALL_LOCKOUT_RISK", "启用系统访问保护前必须保留至少一个 Panel 控制来源")
	}
	byProtocol := map[string][]PortRange{"tcp": {}, "udp": {}}
	ids := make(map[string]struct{}, len(state.Rules))
	for _, rule := range state.Rules {
		if rule.ID == "" {
			return apperr.New("INVALID_FIREWALL_RULE", "防火墙规则缺少 ID")
		}
		if _, exists := ids[rule.ID]; exists {
			return apperr.New("INVALID_FIREWALL_RULE", "防火墙规则 ID 重复")
		}
		ids[rule.ID] = struct{}{}
		if _, err := s.normalizeRule(rule.ID, RuleInput{
			Enabled: rule.Enabled, Protocols: rule.Protocols, Ports: rule.Ports, Sources: rule.Sources, Note: rule.Note,
		}); err != nil {
			return err
		}
		if !rule.Enabled {
			continue
		}
		for _, protocol := range rule.Protocols {
			byProtocol[protocol] = append(byProtocol[protocol], rule.Ports...)
		}
	}
	for protocol, ports := range byProtocol {
		sortPortRanges(ports)
		for index := 1; index < len(ports); index++ {
			if ports[index].From <= ports[index-1].To {
				return apperr.New("FIREWALL_RULE_CONFLICT", "启用规则的协议和端口不能重叠："+protocol)
			}
		}
	}
	return nil
}

func (s *Service) normalizeRule(id string, input RuleInput) (Rule, error) {
	if strings.TrimSpace(id) == "" {
		return Rule{}, apperr.New("INVALID_FIREWALL_RULE", "防火墙规则 ID 无效")
	}
	protocols := normalizeProtocols(input.Protocols)
	if len(protocols) == 0 {
		return Rule{}, apperr.New("INVALID_FIREWALL_RULE", "至少选择一个协议")
	}
	ports := append([]PortRange(nil), input.Ports...)
	if len(ports) == 0 {
		return Rule{}, apperr.New("INVALID_FIREWALL_RULE", "至少指定一个端口或端口范围")
	}
	for _, port := range ports {
		if port.From < 1 || port.From > 65535 || port.To < port.From || port.To > 65535 {
			return Rule{}, apperr.New("INVALID_FIREWALL_RULE", "端口范围必须在 1 到 65535 之间")
		}
		if port.From <= s.port && s.port <= port.To {
			return Rule{}, apperr.New("FIREWALL_PROTECTED_PORT", "daemon 管理端口不能由业务白名单接管")
		}
	}
	sortPortRanges(ports)
	ports = mergePortRanges(ports)
	sources, err := normalizeSources(input.Sources, true)
	if err != nil {
		return Rule{}, err
	}
	if len(sources) == 0 {
		return Rule{}, apperr.New("INVALID_FIREWALL_RULE", "至少指定一个来源 IP 或 CIDR")
	}
	note := strings.TrimSpace(input.Note)
	if len([]rune(note)) > 300 {
		return Rule{}, apperr.New("INVALID_FIREWALL_RULE", "备注不能超过 300 个字符")
	}
	return Rule{ID: id, Enabled: input.Enabled, Protocols: protocols, Ports: ports, Sources: sources, Note: note}, nil
}

func normalizeProtocols(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "tcp" || value == "udp" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for _, protocol := range []string{"tcp", "udp"} {
		if _, exists := set[protocol]; exists {
			result = append(result, protocol)
		}
	}
	return result
}

func normalizeSources(values []string, rejectGlobal bool) ([]string, error) {
	set := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		var prefix netip.Prefix
		if address, err := netip.ParseAddr(value); err == nil {
			address = address.Unmap()
			prefix = netip.PrefixFrom(address, address.BitLen())
		} else {
			parsed, parseErr := netip.ParsePrefix(value)
			if parseErr != nil {
				return nil, apperr.New("INVALID_FIREWALL_SOURCE", "来源 IP 或 CIDR 格式无效")
			}
			prefix = parsed.Masked()
		}
		if prefix.Addr().IsLoopback() || prefix.Addr().IsUnspecified() {
			return nil, apperr.New("INVALID_FIREWALL_SOURCE", "来源不能是回环或未指定地址")
		}
		if rejectGlobal && prefix.Bits() == 0 {
			return nil, apperr.New("INVALID_FIREWALL_SOURCE", "白名单来源不能是全地址空间")
		}
		set[prefix.String()] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func sortPortRanges(values []PortRange) {
	sort.Slice(values, func(left, right int) bool {
		if values[left].From != values[right].From {
			return values[left].From < values[right].From
		}
		return values[left].To < values[right].To
	})
}

func mergePortRanges(values []PortRange) []PortRange {
	if len(values) < 2 {
		return values
	}
	merged := make([]PortRange, 0, len(values))
	for _, current := range values {
		if len(merged) == 0 || current.From > merged[len(merged)-1].To+1 {
			merged = append(merged, current)
			continue
		}
		if current.To > merged[len(merged)-1].To {
			merged[len(merged)-1].To = current.To
		}
	}
	return merged
}

func (s *Service) activeStateLocked() bool {
	if s.state.System.Enabled {
		return true
	}
	for _, rule := range s.state.Rules {
		if rule.Enabled {
			return true
		}
	}
	return false
}

func (s *Service) statusLocked() Status {
	state := "READY"
	if !s.backendState.Supported {
		state = "UNSUPPORTED"
	} else if s.lastError != "" {
		state = "ERROR"
	} else if s.drift {
		state = "DRIFT"
	} else if s.tablePresent {
		state = "APPLIED"
	}
	return Status{
		Supported: s.backendState.Supported, Reason: s.backendState.Reason,
		OS: runtime.GOOS, Architecture: runtime.GOARCH, Backend: s.backendState.Name,
		State: state, Revision: s.state.Revision, DesiredHash: s.state.AppliedHash,
		ObservedHash: s.observedHash, TablePresent: s.tablePresent, Drift: s.drift,
		LastApplied: copyTime(s.state.AppliedAt), LastError: s.lastError,
		System: cloneSystem(s.state.System), GrantCount: len(s.grants),
	}
}

func (s *Service) viewLocked() View {
	return View{Status: s.statusLocked(), Rules: cloneRules(s.state.Rules), Grants: sortedGrants(s.grants)}
}

func (s *Service) expireGrantsLocked(now time.Time) {
	for key, grant := range s.grants {
		if !now.Before(grant.ExpiresAt) {
			delete(s.grants, key)
		}
	}
}

func findRule(rules []Rule, id string) int {
	for index := range rules {
		if rules[index].ID == id {
			return index
		}
	}
	return -1
}

func persistState(path string, state persistedState) error {
	contents, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, append(contents, '\n'), 0o600)
}

func cloneState(input persistedState) persistedState {
	result := input
	result.Rules = cloneRules(input.Rules)
	result.System = cloneSystem(input.System)
	result.AppliedAt = copyTime(input.AppliedAt)
	return result
}

func cloneRules(input []Rule) []Rule {
	result := make([]Rule, len(input))
	for index, rule := range input {
		result[index] = rule
		result[index].Protocols = append([]string(nil), rule.Protocols...)
		result[index].Ports = append([]PortRange(nil), rule.Ports...)
		result[index].Sources = append([]string(nil), rule.Sources...)
	}
	return result
}

func cloneSystem(input SystemAccess) SystemAccess {
	input.ControlSources = append([]string(nil), input.ControlSources...)
	return input
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func sortedGrants(values map[string]Grant) []Grant {
	result := make([]Grant, 0, len(values))
	for _, grant := range values {
		result = append(result, grant)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].ExpiresAt.Equal(result[right].ExpiresAt) {
			return result[left].Source < result[right].Source
		}
		return result[left].ExpiresAt.Before(result[right].ExpiresAt)
	})
	return result
}

func grantKey(source, sessionID, ticketID string) string {
	return source + "\x00" + sessionID + "\x00" + ticketID
}

func hasAnyGrantForSource(values map[string]Grant, source string) bool {
	for _, grant := range values {
		if grant.Source == source {
			return true
		}
	}
	return false
}

func randomID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func hashContents(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func normalizeObservedTable(value string) string {
	lines := strings.Split(value, "\n")
	result := make([]string, 0, len(lines))
	dynamicSetDepth := 0
	skipElementsDepth := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if skipElementsDepth > 0 {
			skipElementsDepth += strings.Count(line, "{") - strings.Count(line, "}")
			if skipElementsDepth < 1 {
				skipElementsDepth = 0
			}
			continue
		}
		if dynamicSetDepth > 0 && strings.HasPrefix(trimmed, "elements") {
			depth := strings.Count(line, "{") - strings.Count(line, "}")
			if depth > 0 {
				skipElementsDepth = depth
			}
			continue
		}
		if dynamicSetDepth == 0 && (strings.HasPrefix(trimmed, "set prismpanel_direct_grants4") || strings.HasPrefix(trimmed, "set prismpanel_direct_grants6")) {
			result = append(result, strings.Join(strings.Fields(trimmed), " "))
			dynamicSetDepth = strings.Count(line, "{") - strings.Count(line, "}")
			continue
		}
		if trimmed == "" {
			continue
		}
		result = append(result, strings.Join(strings.Fields(trimmed), " "))
		if dynamicSetDepth > 0 {
			dynamicSetDepth += strings.Count(line, "{") - strings.Count(line, "}")
			if dynamicSetDepth < 1 {
				dynamicSetDepth = 0
			}
		}
	}
	return strings.Join(result, "\n")
}
