package middleware

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditMalformedAndTruncatedBodiesNeverRetainRawSecrets(t *testing.T) {
	for _, body := range []string{`{"api_key":"planted-secret"`, `{"password":"planted-secret"}`} {
		value, _ := sanitizeAuditRequest([]byte(body), true)
		assert.NotContains(t, value, "planted-secret")
		value, _ = sanitizeAuditRequest([]byte(body[:len(body)-1]), false)
		assert.NotContains(t, value, "planted-secret")
	}
	events := sanitizeAuditResponse([]byte("data: {\"delta\":\"hello\",\"access_token\":\"planted-secret\"}\n\ndata: {\"password\":\"partial-secret"), true)
	encoded, err := common.Marshal(events)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), "hello")
	assert.NotContains(t, string(encoded), "planted-secret")
	assert.NotContains(t, string(encoded), "partial-secret")
}

func TestWebSocketAuditBudgetsResetAndSecretsRedactedBeforeTruncation(t *testing.T) {
	t.Setenv("CHAT_AUDIT_ENABLED", "true")
	t.Setenv("CHAT_AUDIT_DIR", t.TempDir())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses?access_token=query-secret", nil)
	c.Request.Header.Set("Authorization", "Bearer header-secret")
	c.Set(common.RequestIdKey, "session")
	a := NewWebSocketAudit(c)
	require.NotNil(t, a)
	a.limit = 80
	for _, turn := range []string{"first", "second"} {
		a.BeginTurn(turn)
		a.Capture(true, []byte(`{"model":"gpt-test","password":"body-secret","input":"hi"}`))
		a.Capture(false, []byte(`{"delta":"hello","api_key":"body-secret"}`))
		a.Capture(false, []byte(`{"delta":"`+strings.Repeat("x", 90)+`","password":"body-secret"}`))
		assert.LessOrEqual(t, a.response.buffer.Len(), 80)
		for range 1000 {
			a.Capture(false, []byte(`{"delta":"aftercap"}`))
		}
		a.EndTurn("response.completed")
	}
	data, err := os.ReadFile(filepath.Join(a.dir, "audit-"+time.Now().UTC().Format("2006-01-02")+".jsonl"))
	require.NoError(t, err)
	text := string(data)
	for _, secret := range []string{"query-secret", "header-secret", "body-secret"} {
		assert.NotContains(t, text, secret)
	}
	assert.Contains(t, text, `"request_id":"first"`)
	assert.Contains(t, text, `"request_id":"second"`)
	assert.Contains(t, text, `"response_truncated":true`)
	assert.Equal(t, 2, strings.Count(text, "\n"))
	assert.Equal(t, 2, strings.Count(text, `"audit_summary":true`))
	assert.Contains(t, text, `"response_events":1002`)
	assert.Contains(t, text, `"termination":"response.completed"`)
	var summary chatAuditRecord
	lines := strings.Split(strings.TrimSpace(text), "\n")
	require.NoError(t, common.Unmarshal([]byte(lines[len(lines)-1]), &summary))
	expected := len(`{"delta":"hello","api_key":"body-secret"}`) + len(`{"delta":"`+strings.Repeat("x", 90)+`","password":"body-secret"}`) + 1000*len(`{"delta":"aftercap"}`)
	assert.Equal(t, int64(expected), summary.ResponseBytes)
	assert.Equal(t, 1002, summary.ResponseEvents)
	assert.Equal(t, "response.completed", summary.Termination)

}

func TestWebSocketAuditRetainsThousandsOfSmallDeltasWithinByteCap(t *testing.T) {
	t.Setenv("CHAT_AUDIT_ENABLED", "true")
	t.Setenv("CHAT_AUDIT_DIR", t.TempDir())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	a := NewWebSocketAudit(c)
	a.BeginTurn("large-stream")
	for range 2000 {
		a.Capture(false, []byte(`{"type":"response.output_text.delta","delta":"hello"}`))
	}
	assert.False(t, a.response.truncated)
	a.EndTurn("response.completed")
	data, err := os.ReadFile(filepath.Join(a.dir, "audit-"+time.Now().UTC().Format("2006-01-02")+".jsonl"))
	require.NoError(t, err)
	assert.Equal(t, 2000, strings.Count(string(data), "response.output_text.delta"))
	assert.Equal(t, 1, strings.Count(string(data), "\n"))
}

func TestWebSocketAuditFlushesRejectedIdleEventsOnCloseOnce(t *testing.T) {
	t.Setenv("CHAT_AUDIT_ENABLED", "true")
	t.Setenv("CHAT_AUDIT_DIR", t.TempDir())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	a := NewWebSocketAudit(c)
	a.Capture(true, []byte(`{"api_key":"invalid-secret"`))
	a.Capture(false, []byte(`{"type":"error","error":{"message":"invalid event"}}`))
	a.Close()
	a.Close()
	data, err := os.ReadFile(filepath.Join(a.dir, "audit-"+time.Now().UTC().Format("2006-01-02")+".jsonl"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), "\n"))
	assert.NotContains(t, string(data), "invalid-secret")
	var record chatAuditRecord
	require.NoError(t, common.Unmarshal(data, &record))
	assert.NotEmpty(t, record.SessionId)
	assert.Equal(t, "session_closed", record.Termination)
	assert.Equal(t, 1, record.RequestEvents)
	assert.Equal(t, 1, record.ResponseEvents)
}
