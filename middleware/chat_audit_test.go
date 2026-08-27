package middleware

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatAuditRecordsConversationAndRedactsSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	auditDir := t.TempDir()
	t.Setenv("CHAT_AUDIT_ENABLED", "true")
	t.Setenv("CHAT_AUDIT_DIR", auditDir)
	t.Setenv("CHAT_AUDIT_MAX_BODY_MB", "1")

	router := gin.New()
	router.Use(BodyStorageCleanup())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set("id", 12)
		c.Set("username", "audited-user")
		c.Set("token_id", 34)
		c.Set("token_name", "desktop")
		common.SetContextKey(c, constant.ContextKeyChannelId, 56)
		common.SetContextKey(c, constant.ContextKeyChannelName, "pool-a")
		c.Set(common.RequestIdKey, "req-test")
		c.Next()
	}, ChatAudit(), func(c *gin.Context) {
		_, err := common.GetBodyStorage(c)
		require.NoError(t, err)
		c.Header("Content-Type", "application/json")
		c.String(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`)
	})

	body := `{"model":"gpt-test","messages":[{"role":"user","content":"hi"}],"api_key":"must-not-leak"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer sk-must-not-leak")
	request.Header.Set("User-Agent", "audit-test")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)

	file, err := os.Open(filepath.Join(auditDir, "audit-"+time.Now().UTC().Format("2006-01-02")+".jsonl"))
	require.NoError(t, err)
	defer file.Close()
	scanner := bufio.NewScanner(file)
	require.True(t, scanner.Scan())
	line := scanner.Text()
	assert.Contains(t, line, `"request_id":"req-test"`)
	assert.Contains(t, line, `"user_id":12`)
	assert.Contains(t, line, `"token_id":34`)
	assert.Contains(t, line, `"channel_id":56`)
	assert.Contains(t, line, `"model":"gpt-test"`)
	assert.Contains(t, line, `"content":"hi"`)
	assert.Contains(t, line, `"content":"hello"`)
	assert.Contains(t, line, `"api_key":"[REDACTED]"`)
	assert.NotContains(t, line, "must-not-leak")
	require.NoError(t, scanner.Err())
}

func TestChatAuditSkipsNonChatRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	auditDir := t.TempDir()
	t.Setenv("CHAT_AUDIT_ENABLED", "true")
	t.Setenv("CHAT_AUDIT_DIR", auditDir)
	router := gin.New()
	router.Use(ChatAudit())
	router.POST("/v1/embeddings", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"input":"hello"}`)))
	entries, err := os.ReadDir(auditDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestChatAuditCapturesStreamingResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	auditDir := t.TempDir()
	t.Setenv("CHAT_AUDIT_ENABLED", "true")
	t.Setenv("CHAT_AUDIT_DIR", auditDir)
	router := gin.New()
	router.Use(BodyStorageCleanup())
	router.POST("/v1/responses", ChatAudit(), func(c *gin.Context) {
		_, err := common.GetBodyStorage(c)
		require.NoError(t, err)
		c.Header("Content-Type", "text/event-stream")
		_, err = c.Writer.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
		require.NoError(t, err)
		c.Writer.Flush()
		_, err = c.Writer.WriteString("data: [DONE]\n\n")
		require.NoError(t, err)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test","input":"hi","stream":true}`)))
	require.Equal(t, http.StatusOK, response.Code)
	line, err := os.ReadFile(filepath.Join(auditDir, "audit-"+time.Now().UTC().Format("2006-01-02")+".jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(line), `response.output_text.delta`)
	assert.Contains(t, string(line), `[DONE]`)
}
