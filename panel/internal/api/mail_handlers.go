package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"PrismPanel/internal/playerdata"
)

type mailAttachmentInput struct {
	ItemKey string `json:"item_key"`
	Amount  int64  `json:"amount"`
}

type mailSendInput struct {
	ClientRequestID string                `json:"client_request_id"`
	Type            string                `json:"type"`
	SenderUUID      string                `json:"sender_uuid,omitempty"`
	Recipients      []string              `json:"recipients,omitempty"`
	Broadcast       bool                  `json:"broadcast"`
	Subject         string                `json:"subject"`
	Body            string                `json:"body"`
	Attachments     []mailAttachmentInput `json:"attachments,omitempty"`
}

func (s *Server) handleFeatures(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	writeSuccess(writer, map[string]bool{"mail": s.config.Features.Mail})
}

func (s *Server) handleMailSend(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	if !s.config.Features.Mail {
		err := apiError("FEATURE_DISABLED", "邮件功能未启用")
		s.record(request, "mail.send", "", nil, err)
		writeRequestError(writer, err)
		return
	}
	var input mailSendInput
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	if err == nil {
		err = validateMailSendInput(&input)
	}
	detail := map[string]any{
		"type": input.Type, "broadcast": input.Broadcast,
		"recipient_count": len(input.Recipients), "attachment_count": len(input.Attachments),
	}
	if err != nil {
		s.record(request, "mail.send", "", detail, err)
		writeRequestError(writer, publicError(err))
		return
	}
	if input.ClientRequestID == "" {
		input.ClientRequestID = "panel-" + requestID(request)
	}
	payload := map[string]any{
		"client_request_id": input.ClientRequestID, "type": input.Type,
		"broadcast": input.Broadcast, "subject": input.Subject, "body": input.Body,
	}
	if input.SenderUUID != "" {
		payload["sender_uuid"] = input.SenderUUID
	}
	if len(input.Recipients) > 0 {
		payload["recipients"] = input.Recipients
	}
	if len(input.Attachments) > 0 {
		payload["attachments"] = input.Attachments
	}
	var data json.RawMessage
	if s.playerData == nil {
		err = apiError("PLAYERDATA_UNAVAILABLE", "PlayerData 邮件服务当前不可用")
	} else {
		data, err = s.playerData.Send(request.Context(), payload)
	}
	if err != nil {
		err = playerDataError(err)
	}
	s.record(request, "mail.send", input.Type, detail, err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	var result any
	if len(data) > 0 && json.Unmarshal(data, &result) == nil {
		writeSuccess(writer, result)
		return
	}
	writeSuccess(writer, map[string]any{})
}

func validateMailSendInput(input *mailSendInput) error {
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	input.Subject = strings.TrimSpace(input.Subject)
	input.Body = strings.TrimSpace(input.Body)
	input.SenderUUID = strings.TrimSpace(input.SenderUUID)
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)
	if input.Type != "system" && input.Type != "admin" {
		return apiError("INVALID_REQUEST", "邮件类型必须是 system 或 admin")
	}
	if input.Subject == "" || len([]rune(input.Subject)) > 128 {
		return apiError("INVALID_REQUEST", "邮件标题不能为空且不能超过 128 个字符")
	}
	if input.Body == "" || len([]rune(input.Body)) > 8192 {
		return apiError("INVALID_REQUEST", "邮件正文不能为空且不能超过 8192 个字符")
	}
	if input.Type == "system" && input.SenderUUID != "" {
		return apiError("INVALID_REQUEST", "系统邮件不能填写发送者")
	}
	if input.Type == "admin" && input.SenderUUID == "" {
		return apiError("INVALID_REQUEST", "管理员邮件必须填写发送者 UUID")
	}
	if input.Broadcast && len(input.Recipients) > 0 {
		return apiError("INVALID_REQUEST", "全体邮件不能同时填写收件人")
	}
	if !input.Broadcast && len(input.Recipients) == 0 {
		return apiError("INVALID_REQUEST", "非全体邮件至少需要一个收件人")
	}
	if len(input.Recipients) > 1000 || len(input.Attachments) > 32 {
		return apiError("INVALID_REQUEST", "收件人或附件数量超过限制")
	}
	for index := range input.Recipients {
		input.Recipients[index] = strings.TrimSpace(input.Recipients[index])
		if input.Recipients[index] == "" {
			return apiError("INVALID_REQUEST", fmt.Sprintf("第 %d 个收件人 UUID 为空", index+1))
		}
	}
	for index := range input.Attachments {
		input.Attachments[index].ItemKey = strings.TrimSpace(input.Attachments[index].ItemKey)
		if input.Attachments[index].ItemKey == "" || input.Attachments[index].Amount <= 0 {
			return apiError("INVALID_REQUEST", fmt.Sprintf("第 %d 个附件无效", index+1))
		}
	}
	return nil
}

func playerDataError(err error) error {
	var remote *playerdata.Error
	if errors.As(err, &remote) {
		if remote.StatusCode >= 400 && remote.StatusCode < 500 {
			return apiError("PLAYERDATA_ERROR", "PlayerData 拒绝了邮件请求")
		}
		return apiError("PLAYERDATA_ERROR", "PlayerData 邮件服务返回错误")
	}
	return apiError("PLAYERDATA_UNAVAILABLE", "PlayerData 邮件服务当前不可用")
}
