package protocol

import "encoding/json"

type Incoming struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	Token     string          `json:"token,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type Outgoing struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id,omitempty"`
	Success   *bool  `json:"success,omitempty"`
	Data      any    `json:"data,omitempty"`
	Error     any    `json:"error,omitempty"`
}

func Response(requestID string, data any) Outgoing {
	success := true
	return Outgoing{Type: "response", RequestID: requestID, Success: &success, Data: data}
}

func Failure(requestID string, apiError any) Outgoing {
	success := false
	return Outgoing{Type: "response", RequestID: requestID, Success: &success, Error: apiError}
}

func Event(eventType string, data any) Outgoing {
	return Outgoing{Type: eventType, Data: data}
}
