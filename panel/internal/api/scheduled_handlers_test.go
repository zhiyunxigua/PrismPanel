package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestTaskRunCursorRoundTrip(t *testing.T) {
	createdAt := time.Date(2026, 8, 5, 12, 30, 45, 123000000, time.UTC)
	cursor := encodeTaskRunCursor(taskRunCursor{CreatedAt: createdAt, ID: "run-123"})
	request := httptest.NewRequest("GET", "/api/v1/task-runs?cursor="+cursor, nil)
	filter, err := parseTaskRunFilter(request)
	if err != nil {
		t.Fatal(err)
	}
	if filter.CursorCreatedAt == nil || !filter.CursorCreatedAt.Equal(createdAt) ||
		filter.CursorID != "run-123" {
		t.Fatalf("unexpected cursor filter: %#v", filter)
	}
}

func TestTaskRunFilterRejectsInvalidStatus(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/task-runs?status=unknown", nil)
	if _, err := parseTaskRunFilter(request); err == nil {
		t.Fatal("expected invalid status error")
	}
}
