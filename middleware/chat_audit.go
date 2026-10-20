package middleware

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"

	"github.com/gin-gonic/gin"
)

const defaultChatAuditMaxBodyMB = 8

var chatAuditWriteMu sync.Mutex

type chatAuditRecord struct {
	Version           int         `json:"version"`
	SessionId         string      `json:"session_id,omitempty"`
	AuditSummary      bool        `json:"audit_summary,omitempty"`
	Termination       string      `json:"termination,omitempty"`
	RequestEvents     int         `json:"request_events,omitempty"`
	ResponseEvents    int         `json:"response_events,omitempty"`
	StartedAt         string      `json:"started_at"`
	FinishedAt        string      `json:"finished_at"`
	DurationMs        int64       `json:"duration_ms"`
	RequestId         string      `json:"request_id"`
	UpstreamRequestId string      `json:"upstream_request_id,omitempty"`
	UserId            int         `json:"user_id"`
	Username          string      `json:"username,omitempty"`
	TokenId           int         `json:"token_id"`
	TokenName         string      `json:"token_name,omitempty"`
	ClientIp          string      `json:"client_ip"`
	UserAgent         string      `json:"user_agent,omitempty"`
	Method            string      `json:"method"`
	Path              string      `json:"path"`
	Model             string      `json:"model,omitempty"`
	ChannelId         int         `json:"channel_id,omitempty"`
	ChannelName       string      `json:"channel_name,omitempty"`
	Status            int         `json:"status"`
	RequestBytes      int64       `json:"request_bytes"`
	ResponseBytes     int64       `json:"response_bytes"`
	RequestTruncated  bool        `json:"request_truncated,omitempty"`
	ResponseTruncated bool        `json:"response_truncated,omitempty"`
	Request           interface{} `json:"request"`
	Response          interface{} `json:"response"`
}

type cappedAuditBuffer struct {
	buffer    bytes.Buffer
	limit     int64
	total     int64
	truncated bool
}

func (b *cappedAuditBuffer) capture(data []byte) {
	b.total += int64(len(data))
	remaining := b.limit - int64(b.buffer.Len())
	if remaining <= 0 {
		b.truncated = true
		return
	}
	if int64(len(data)) > remaining {
		_, _ = b.buffer.Write(data[:remaining])
		b.truncated = true
		return
	}
	_, _ = b.buffer.Write(data)
}

type chatAuditResponseWriter struct {
	gin.ResponseWriter
	capture *cappedAuditBuffer
}

func (w *chatAuditResponseWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if n > 0 {
		w.capture.capture(data[:n])
	}
	return n, err
}

func (w *chatAuditResponseWriter) WriteString(data string) (int, error) {
	n, err := w.ResponseWriter.WriteString(data)
	if n > 0 {
		w.capture.capture([]byte(data[:n]))
	}
	return n, err
}

func ChatAudit() gin.HandlerFunc {
	enabled := strings.EqualFold(strings.TrimSpace(os.Getenv("CHAT_AUDIT_ENABLED")), "true")
	auditDir := strings.TrimSpace(os.Getenv("CHAT_AUDIT_DIR"))
	if auditDir == "" {
		auditDir = "/data/chat-audit"
	}
	maxBodyMB := defaultChatAuditMaxBodyMB
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv("CHAT_AUDIT_MAX_BODY_MB"))); err == nil && value > 0 && value <= 128 {
		maxBodyMB = value
	}
	maxBodyBytes := int64(maxBodyMB) * 1024 * 1024

	return func(c *gin.Context) {
		if !enabled || !isChatAuditPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		startedAt := time.Now().UTC()
		responseCapture := &cappedAuditBuffer{limit: maxBodyBytes}
		originalWriter := c.Writer
		c.Writer = &chatAuditResponseWriter{ResponseWriter: originalWriter, capture: responseCapture}
		c.Next()
		c.Writer = originalWriter

		requestBody, requestBytes, requestTruncated, err := readAuditRequestBody(c, maxBodyBytes)
		if err != nil {
			logger.LogError(c, "chat audit cannot read request body: "+err.Error())
			requestBody = []byte(fmt.Sprintf("[audit read failed: %s]", err.Error()))
		}
		requestValue, modelName := sanitizeAuditRequest(requestBody, requestTruncated)
		finishedAt := time.Now().UTC()
		record := chatAuditRecord{
			Version:           1,
			StartedAt:         startedAt.Format(time.RFC3339Nano),
			FinishedAt:        finishedAt.Format(time.RFC3339Nano),
			DurationMs:        finishedAt.Sub(startedAt).Milliseconds(),
			RequestId:         c.GetString(common.RequestIdKey),
			UpstreamRequestId: c.GetString(common.UpstreamRequestIdKey),
			UserId:            c.GetInt(string(constant.ContextKeyUserId)),
			Username:          c.GetString("username"),
			TokenId:           c.GetInt(string(constant.ContextKeyTokenId)),
			TokenName:         c.GetString("token_name"),
			ClientIp:          c.ClientIP(),
			UserAgent:         c.Request.UserAgent(),
			Method:            c.Request.Method,
			Path:              c.Request.URL.Path,
			Model:             modelName,
			ChannelId:         common.GetContextKeyInt(c, constant.ContextKeyChannelId),
			ChannelName:       common.GetContextKeyString(c, constant.ContextKeyChannelName),
			Status:            originalWriter.Status(),
			RequestBytes:      requestBytes,
			ResponseBytes:     responseCapture.total,
			RequestTruncated:  requestTruncated,
			ResponseTruncated: responseCapture.truncated,
			Request:           requestValue,
			Response:          sanitizeAuditResponse(responseCapture.buffer.Bytes(), responseCapture.truncated),
		}
		if record.RequestId == "" {
			record.RequestId = common.NewRequestId()
		}
		if err := appendChatAuditRecord(auditDir, startedAt, record); err != nil {
			logger.LogError(c, "chat audit write failed: "+err.Error())
		}
	}
}

func isChatAuditPath(path string) bool {
	switch path {
	case "/v1/messages", "/v1/completions", "/v1/chat/completions", "/v1/responses", "/v1/responses/compact":
		return true
	default:
		return strings.HasPrefix(path, "/v1beta/models/") &&
			(strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent"))
	}
}

func readAuditRequestBody(c *gin.Context, limit int64) ([]byte, int64, bool, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, 0, false, err
	}
	if _, err = storage.Seek(0, io.SeekStart); err != nil {
		return nil, storage.Size(), false, err
	}
	data, err := io.ReadAll(io.LimitReader(storage, limit+1))
	if err != nil {
		return nil, storage.Size(), false, err
	}
	truncated := int64(len(data)) > limit
	if truncated {
		data = data[:limit]
	}
	return data, storage.Size(), truncated, nil
}

func sanitizeAuditRequest(body []byte, truncated bool) (interface{}, string) {
	if truncated {
		return "[audit body omitted: incomplete or invalid JSON]", ""
	}
	var value interface{}
	if err := common.Unmarshal(body, &value); err != nil {
		return "[audit body omitted: incomplete or invalid JSON]", ""
	}
	value = redactAuditSecrets(value)
	modelName := ""
	if object, ok := value.(map[string]interface{}); ok {
		if model, ok := object["model"].(string); ok {
			modelName = model
		}
	}
	return value, modelName
}

func sanitizeAuditResponse(body []byte, truncated bool) interface{} {
	// SSE is parsed one complete event at a time; never retain raw malformed or
	// partial data, because a secret field may cross the capture boundary.
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("data:")) || bytes.HasPrefix(body, []byte("event:")) {
		var events []any
		for _, event := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n\n") {
			var data []string
			for line := range strings.SplitSeq(event, "\n") {
				if value, ok := strings.CutPrefix(line, "data:"); ok {
					data = append(data, strings.TrimPrefix(value, " "))
				}
			}
			if len(data) == 0 {
				continue
			}
			payload := strings.Join(data, "\n")
			if payload == "[DONE]" {
				events = append(events, payload)
				continue
			}
			value, _ := sanitizeAuditRequest([]byte(payload), false)
			events = append(events, value)
		}
		return events
	}
	value, _ := sanitizeAuditRequest(body, truncated)
	return value
}

func redactAuditSecrets(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if isAuditSecretKey(key) {
				typed[key] = "[REDACTED]"
				continue
			}
			typed[key] = redactAuditSecrets(child)
		}
	case []interface{}:
		for index, child := range typed {
			typed[index] = redactAuditSecrets(child)
		}
	}
	return value
}

func isAuditSecretKey(key string) bool {
	normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	switch normalized {
	case "authorization", "proxyauthorization", "apikey", "xapikey", "xgoogapikey", "accesstoken", "refreshtoken", "idtoken", "clientsecret", "password", "secret", "cookie", "setcookie", "sessiontoken":
		return true
	default:
		return false
	}
}

func appendChatAuditRecord(auditDir string, startedAt time.Time, record chatAuditRecord) error {
	line, err := common.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}
	line = append(line, '\n')

	chatAuditWriteMu.Lock()
	defer chatAuditWriteMu.Unlock()
	if err := os.MkdirAll(auditDir, 0o700); err != nil {
		return fmt.Errorf("create audit directory: %w", err)
	}
	path := filepath.Join(auditDir, "audit-"+startedAt.UTC().Format("2006-01-02")+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open audit file: %w", err)
	}
	defer file.Close()
	if _, err = file.Write(line); err != nil {
		return fmt.Errorf("append audit file: %w", err)
	}
	return nil
}
