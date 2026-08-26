package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"PrismPanel/internal/daemon"
)

func TestParseBatchRequestValidInstanceActions(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart", "kill"} {
		body := []byte(`{"action":"` + action + `","targets":[{"node_id":"node-a","instance_id":"srv_1"}]}`)
		req, err := parseBatchRequest(body)
		if err != nil {
			t.Fatalf("action %s: %v", action, err)
		}
		if req.Action != action || len(req.Targets) != 1 {
			t.Fatalf("action %s: unexpected parse result %#v", action, req)
		}
		if req.Targets[0].NodeID != "node-a" || req.Targets[0].InstanceID != "srv_1" {
			t.Fatalf("action %s: target mismatch %#v", action, req.Targets[0])
		}
	}
}

func TestParseBatchRequestServerScopedTarget(t *testing.T) {
	body := []byte(`{"action":"restart","targets":[{"node_id":"node-a","server_id":"group-x"}]}`)
	req, err := parseBatchRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Targets[0].ServerID != "group-x" || req.Targets[0].InstanceID != "" {
		t.Fatalf("server-scoped target mismatch: %#v", req.Targets[0])
	}
}

func TestParseBatchRequestRejectsInvalidAction(t *testing.T) {
	body := []byte(`{"action":"explode","targets":[{"node_id":"node-a","instance_id":"srv_1"}]}`)
	if _, err := parseBatchRequest(body); err == nil {
		t.Fatal("expected invalid action rejection")
	}
}

func TestParseBatchRequestDeleteRequiresConfirm(t *testing.T) {
	body := []byte(`{"action":"delete","targets":[{"node_id":"node-a","server_id":"group-x"}]}`)
	if _, err := parseBatchRequest(body); err == nil {
		t.Fatal("expected delete without confirm rejection")
	}
	body = []byte(`{"action":"delete","confirm":true,"targets":[{"node_id":"node-a","server_id":"group-x"}]}`)
	if _, err := parseBatchRequest(body); err != nil {
		t.Fatalf("delete with confirm: %v", err)
	}
}

func TestParseBatchRequestDeleteRequiresServerID(t *testing.T) {
	body := []byte(`{"action":"delete","confirm":true,"targets":[{"node_id":"node-a","instance_id":"srv_1"}]}`)
	if _, err := parseBatchRequest(body); err == nil {
		t.Fatal("expected delete instance-scoped rejection")
	}
	req, err := parseBatchRequest([]byte(`{"action":"delete","confirm":true,"targets":[{"node_id":"node-a","server_id":"group-x","instance_id":"srv_1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.Targets[0].InstanceID != "" {
		t.Fatal("expected delete to strip instance_id")
	}
}

func TestParseBatchRequestValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty targets", `{"action":"start","targets":[]}`},
		{"missing node id", `{"action":"start","targets":[{"instance_id":"srv_1"}]}`},
		{"missing both selectors", `{"action":"start","targets":[{"node_id":"node-a"}]}`},
		{"bad json", `{"action":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseBatchRequest([]byte(tc.body)); err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			}
		})
	}
}

func TestParseBatchRequestTrimsAndDeduplicates(t *testing.T) {
	body := []byte(`{"action":"start","targets":[
		{"node_id":" node-a ","instance_id":" srv_1 "},
		{"node_id":"node-a","server_id":"group-x"}
	]}`)
	req, err := parseBatchRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Targets[0].NodeID != "node-a" || req.Targets[0].InstanceID != "srv_1" {
		t.Fatalf("trim failed: %#v", req.Targets[0])
	}
	dup := []byte(`{"action":"start","targets":[
		{"node_id":"node-a","instance_id":"srv_1"},
		{"node_id":"node-a","instance_id":"srv_1"}
	]}`)
	if _, err := parseBatchRequest(dup); err == nil {
		t.Fatal("expected duplicate target rejection")
	}
}

func TestParseBatchRequestMaxTargets(t *testing.T) {
	var builder strings.Builder
	builder.WriteString(`{"action":"start","targets":[`)
	for i := 0; i <= maxBatchTargets; i++ {
		if i > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(`{"node_id":"node-a","instance_id":"srv_`)
		builder.WriteString(strings.Repeat("x", i+1))
		builder.WriteString(`"}`)
	}
	builder.WriteString(`]}`)
	if _, err := parseBatchRequest([]byte(builder.String())); err == nil {
		t.Fatal("expected max targets rejection")
	}
}

func TestSummarizeBatch(t *testing.T) {
	results := []batchResult{
		{NodeID: "a", Success: true},
		{NodeID: "b", Success: false, Error: apiError("FORBIDDEN", "无权")},
		{NodeID: "c", Success: true},
	}
	summary := summarizeBatch(results)
	if summary.Total != 3 || summary.Succeeded != 2 || summary.Failed != 1 {
		t.Fatalf("unexpected summary %#v", summary)
	}
	if summarizeBatch(nil).Total != 0 {
		t.Fatal("expected empty summary")
	}
}

func TestAPIErrorFrom(t *testing.T) {
	if err := apiErrorFrom(nil); err != nil {
		t.Fatalf("nil should map to nil, got %v", err)
	}
	if got := apiErrorFrom(apiError("FORBIDDEN", "无权")); got == nil || got.Code != "FORBIDDEN" {
		t.Fatalf("panel error passthrough failed: %v", got)
	}
	if got := apiErrorFrom(&daemon.APIError{Code: "INSTANCE_BUSY", Message: "busy"}); got == nil || got.Code != "INSTANCE_BUSY" {
		t.Fatalf("daemon error mapping failed: %v", got)
	}
	if got := apiErrorFrom(daemon.ErrDisconnected); got == nil || got.Code != "DAEMON_UNAVAILABLE" {
		t.Fatalf("disconnected mapping failed: %v", got)
	}
	if got := apiErrorFrom(context.DeadlineExceeded); got == nil || got.Code != "DAEMON_TIMEOUT" {
		t.Fatalf("timeout mapping failed: %v", got)
	}
	if got := apiErrorFrom(errors.New("boom")); got == nil || got.Code != "INTERNAL" {
		t.Fatalf("generic mapping failed: %v", got)
	}
}

func TestBatchResourceTarget(t *testing.T) {
	if got := batchResourceTarget([]batchTarget{{NodeID: "a", InstanceID: "srv_1"}}); got != "a:srv_1" {
		t.Fatalf("instance target summary = %q", got)
	}
	if got := batchResourceTarget([]batchTarget{{NodeID: "a", ServerID: "g"}}); got != "a:g" {
		t.Fatalf("server target summary = %q", got)
	}
	if got := batchResourceTarget([]batchTarget{{NodeID: "a", ServerID: "g"}, {NodeID: "b", ServerID: "h"}}); got != "batch(2)" {
		t.Fatalf("multi target summary = %q", got)
	}
}
