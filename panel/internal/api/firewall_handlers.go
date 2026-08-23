package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type firewallNodeItem struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	Capabilities []string `json:"capabilities"`
}

type firewallRuleInput struct {
	Enabled   bool        `json:"enabled"`
	Protocols []string    `json:"protocols"`
	Ports     []portRange `json:"ports"`
	Sources   []string    `json:"sources"`
	Note      string      `json:"note"`
}

type portRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type firewallRuleMutation struct {
	ExpectedRevision int64             `json:"expected_revision"`
	Rule             firewallRuleInput `json:"rule"`
}

type firewallRevisionInput struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

type firewallSystemMutation struct {
	ExpectedRevision int64 `json:"expected_revision"`
	System           struct {
		Enabled             bool     `json:"enabled"`
		ControlSources      []string `json:"control_sources"`
		GrantTTLSeconds     int      `json:"grant_ttl_seconds"`
		IncludeCallerSource bool     `json:"include_caller_source"`
	} `json:"system"`
}

func (s *Server) handleFirewall(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/firewall/"), "/")
	if path == "nodes" {
		s.handleFirewallNodes(writer, request)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] != "nodes" || strings.TrimSpace(parts[1]) == "" {
		http.NotFound(writer, request)
		return
	}
	nodeID := strings.TrimSpace(parts[1])
	if _, err := s.nodes.Get(request.Context(), nodeID); err != nil {
		writeRequestError(writer, publicError(err))
		return
	}
	if len(parts) == 2 {
		s.handleFirewallView(writer, request, nodeID)
		return
	}
	switch {
	case len(parts) == 3 && parts[2] == "rules":
		s.handleFirewallRuleCreate(writer, request, nodeID)
	case len(parts) == 4 && parts[2] == "rules" && strings.TrimSpace(parts[3]) != "":
		s.handleFirewallRule(writer, request, nodeID, strings.TrimSpace(parts[3]))
	case len(parts) == 3 && parts[2] == "system":
		s.handleFirewallSystem(writer, request, nodeID)
	default:
		http.NotFound(writer, request)
	}
}

func (s *Server) handleFirewallNodes(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	if err := s.authorize(request, "firewall.view"); err != nil {
		writeRequestError(writer, err)
		return
	}
	nodes, err := s.nodes.List(request.Context())
	if err != nil {
		writeRequestError(writer, publicError(err))
		return
	}
	items := make([]firewallNodeItem, len(nodes))
	for index, node := range nodes {
		items[index] = firewallNodeItem{
			ID: node.ID, Name: node.Name, Status: node.Status,
			Capabilities: append([]string(nil), node.Capabilities...),
		}
	}
	writeSuccess(writer, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleFirewallView(writer http.ResponseWriter, request *http.Request, nodeID string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	if err := s.authorize(request, "firewall.view"); err != nil {
		writeRequestError(writer, err)
		return
	}
	var result json.RawMessage
	if err := s.callFirewall(request.Context(), nodeID, "firewall.list", map[string]any{}, &result); err != nil {
		writeRequestError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (s *Server) handleFirewallRuleCreate(writer http.ResponseWriter, request *http.Request, nodeID string) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	if err := s.authorize(request, "firewall.manage"); err != nil {
		writeRequestError(writer, err)
		return
	}
	var input firewallRuleMutation
	err := decodeFirewallInput(request, &input)
	var result json.RawMessage
	if err == nil {
		err = s.callFirewall(request.Context(), nodeID, "firewall.rule.create", input, &result)
	}
	s.record(request, "firewall.rule.create", nodeID, map[string]any{
		"node_id": nodeID, "expected_revision": input.ExpectedRevision, "rule": input.Rule,
	}, err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, response{Success: true, Data: result})
}

func (s *Server) handleFirewallRule(writer http.ResponseWriter, request *http.Request, nodeID, ruleID string) {
	if err := s.authorize(request, "firewall.manage"); err != nil {
		writeRequestError(writer, err)
		return
	}
	switch request.Method {
	case http.MethodPut:
		var input firewallRuleMutation
		err := decodeFirewallInput(request, &input)
		var result json.RawMessage
		if err == nil {
			err = s.callFirewall(request.Context(), nodeID, "firewall.rule.update", map[string]any{
				"rule_id": ruleID, "expected_revision": input.ExpectedRevision, "rule": input.Rule,
			}, &result)
		}
		s.record(request, "firewall.rule.update", ruleID, map[string]any{
			"node_id": nodeID, "expected_revision": input.ExpectedRevision, "rule": input.Rule,
		}, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, result)
	case http.MethodDelete:
		var input firewallRevisionInput
		err := decodeFirewallInput(request, &input)
		var result json.RawMessage
		if err == nil {
			err = s.callFirewall(request.Context(), nodeID, "firewall.rule.delete", map[string]any{
				"rule_id": ruleID, "expected_revision": input.ExpectedRevision,
			}, &result)
		}
		s.record(request, "firewall.rule.delete", ruleID, map[string]any{
			"node_id": nodeID, "expected_revision": input.ExpectedRevision,
		}, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, result)
	default:
		methodNotAllowed(writer, "PUT, DELETE")
	}
}

func (s *Server) handleFirewallSystem(writer http.ResponseWriter, request *http.Request, nodeID string) {
	if request.Method != http.MethodPut {
		methodNotAllowed(writer, "PUT")
		return
	}
	if err := s.authorize(request, "firewall.manage"); err != nil {
		writeRequestError(writer, err)
		return
	}
	var input firewallSystemMutation
	err := decodeFirewallInput(request, &input)
	input.System.IncludeCallerSource = true
	var result json.RawMessage
	if err == nil {
		err = s.callFirewall(request.Context(), nodeID, "firewall.system.configure", input, &result)
	}
	s.record(request, "firewall.system.configure", nodeID, map[string]any{
		"node_id": nodeID, "expected_revision": input.ExpectedRevision,
		"enabled": input.System.Enabled, "control_sources": input.System.ControlSources,
		"grant_ttl_seconds": input.System.GrantTTLSeconds,
	}, err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (s *Server) callFirewall(parent context.Context, nodeID, command string, input, output any) error {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	return s.connections.Call(ctx, nodeID, command, input, output)
}

func decodeFirewallInput(request *http.Request, target any) error {
	body, err := readBody(request)
	if err != nil {
		return apiError("INVALID_REQUEST", "无法读取防火墙请求")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return apiError("INVALID_REQUEST", "防火墙请求格式无效")
	}
	return nil
}
