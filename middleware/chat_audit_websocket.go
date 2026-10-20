package middleware

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/gin-gonic/gin"
)

// WebSocketAudit stores bounded, individually sanitized application events.
// Headers, query strings and upstream connection state are never serialized.
// One instance belongs to a session; BeginTurn resets the capture budget.
type WebSocketAudit struct {
	requestEvents, responseEvents         int
	requestSuppressed, responseSuppressed bool
	dirty                                 bool
	mu                                    sync.Mutex
	dir                                   string
	limit                                 int64
	request, response                     cappedAuditBuffer
	record                                chatAuditRecord
	session                               chatAuditRecord
	started                               time.Time
}

func NewWebSocketAudit(c *gin.Context) *WebSocketAudit {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("CHAT_AUDIT_ENABLED")), "true") {
		return nil
	}
	dir := strings.TrimSpace(os.Getenv("CHAT_AUDIT_DIR"))
	if dir == "" {
		dir = "/data/chat-audit"
	}
	maxMB := defaultChatAuditMaxBodyMB
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("CHAT_AUDIT_MAX_BODY_MB"))); err == nil && n > 0 && n <= 128 {
		maxMB = n
	}
	now := time.Now().UTC()
	a := &WebSocketAudit{dir: dir, limit: int64(maxMB) << 20, started: now}
	a.record = chatAuditRecord{Version: 1, StartedAt: now.Format(time.RFC3339Nano), RequestId: c.GetString(common.RequestIdKey), UserId: c.GetInt("id"), TokenId: c.GetInt("token_id"), ClientIp: c.ClientIP(), Method: "WEBSOCKET", Path: "/v1/responses", Status: 101}
	a.record.SessionId = a.record.RequestId
	if a.record.SessionId == "" {
		a.record.SessionId = common.NewRequestId()
	}
	a.session = a.record
	a.request.limit, a.response.limit = a.limit, a.limit
	return a
}

func (a *WebSocketAudit) BeginTurn(requestID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dirty {
		a.finish("session_events")
	}
	a.reset()
	a.record.RequestId = requestID
}

func (a *WebSocketAudit) reset() {
	a.record = a.session
	a.started = time.Now().UTC()
	a.record.StartedAt = a.started.Format(time.RFC3339Nano)
	a.request = cappedAuditBuffer{limit: a.limit}
	a.response = cappedAuditBuffer{limit: a.limit}
	a.requestEvents, a.responseEvents = 0, 0
	a.requestSuppressed, a.responseSuppressed = false, false
	a.dirty = false
}

func (a *WebSocketAudit) SetContext(c *gin.Context) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.record.Username = c.GetString("username")
	a.record.TokenName = c.GetString("token_name")
	a.record.UserId = c.GetInt("id")
	a.record.TokenId = c.GetInt("token_id")
	a.record.ChannelId = common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	a.record.ChannelName = common.GetContextKeyString(c, constant.ContextKeyChannelName)
	a.record.UpstreamRequestId = c.GetString(common.UpstreamRequestIdKey)
}

// Capture is called at client message admission or after a successful socket
// write. Whole sanitized events are buffered within each direction's byte cap;
// no per-frame file I/O or duplicate metadata competes with streaming latency.
func (a *WebSocketAudit) Capture(request bool, body []byte) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dirty = true
	capture, events, suppressed := &a.response, &a.responseEvents, &a.responseSuppressed
	if request {
		capture, events, suppressed = &a.request, &a.requestEvents, &a.requestSuppressed
	}
	capture.total += int64(len(body))
	*events++
	if *suppressed {
		return
	}
	value, modelName := sanitizeAuditRequest(body, false)
	sanitized, err := common.Marshal(value)
	if err != nil {
		return
	}
	if modelName != "" {
		a.record.Model = modelName
	}
	// Leave room for array delimiters. Never save a partial JSON event.
	if int64(capture.buffer.Len()+len(sanitized)+3) > capture.limit {
		*suppressed = true
		capture.truncated = true
		return
	}
	if capture.buffer.Len() > 0 {
		capture.buffer.WriteByte(',')
	}
	capture.buffer.Write(sanitized)
}

// EndTurn runs after the terminal frame has been published and the inner
// middleware has completed, so final counters and channel metadata agree.
func (a *WebSocketAudit) EndTurn(termination string, status ...int) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(status) > 0 {
		a.record.Status = status[0]
	}
	a.finish(termination)
	a.reset()
}

// Close flushes bounded events that have no admitted turn, such as malformed
// messages or upstream events arriving while the session is idle.
func (a *WebSocketAudit) Close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dirty {
		a.finish("session_closed")
		a.dirty = false
	}
}

func (a *WebSocketAudit) finish(termination string) {
	r := a.record
	r.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	r.DurationMs = time.Since(a.started).Milliseconds()
	r.RequestBytes, r.ResponseBytes = a.request.total, a.response.total
	r.RequestTruncated, r.ResponseTruncated = a.request.truncated, a.response.truncated
	r.RequestEvents, r.ResponseEvents = a.requestEvents, a.responseEvents
	r.AuditSummary = true
	r.Termination = termination
	r.Request = common.RawMessage("[" + a.request.buffer.String() + "]")
	r.Response = common.RawMessage("[" + a.response.buffer.String() + "]")
	if err := appendChatAuditRecord(a.dir, a.started, r); err != nil {
		logger.LogError(nil, "websocket chat audit write failed: "+err.Error())
	}
}
