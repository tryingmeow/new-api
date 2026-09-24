package router

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The chat-audit hook is attached where each chat route is registered, so an
// upstream change that re-registers a chat endpoint elsewhere silently drops it
// from the audit. This drives authenticated requests through the production
// relay wiring and requires exactly one record for every path that
// middleware.isChatAuditPath lists, and none for a non-chat relay path.
func TestChatAuditRecordsEveryChatPathThroughRelayRouting(t *testing.T) {
	setupRelayRouterTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}))
	require.NoError(t, i18n.Init())

	auditDir := t.TempDir()
	t.Setenv("CHAT_AUDIT_ENABLED", "true")
	t.Setenv("CHAT_AUDIT_DIR", auditDir)
	t.Setenv("CHAT_AUDIT_MAX_BODY_MB", "1")

	user := model.User{Username: "audit-router-user", Status: common.UserStatusEnabled, Group: "default", Quota: 100}
	require.NoError(t, model.DB.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "auditroutertestkey", Name: "audit-router", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, model.DB.Create(&token).Error)

	// Same registration order as SetRouter for every route that serves chat traffic.
	engine := gin.New()
	SetRelayRouter(engine)
	SetTaskPluginProtocolRouter(engine)

	chatBody := `{"model":"gpt-audit","messages":[{"role":"user","content":"hi"}],"input":"hi","prompt":"hi","max_tokens":8}`
	geminiBody := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	requests := []struct {
		path    string
		body    string
		audited bool
	}{
		{path: "/v1/messages", body: chatBody, audited: true},
		{path: "/v1/completions", body: chatBody, audited: true},
		{path: "/v1/chat/completions", body: chatBody, audited: true},
		{path: "/v1/responses", body: chatBody, audited: true},
		{path: "/v1/responses/compact", body: chatBody, audited: true},
		{path: "/v1beta/models/gemini-audit:generateContent", body: geminiBody, audited: true},
		{path: "/v1beta/models/gemini-audit:streamGenerateContent", body: geminiBody, audited: true},
		{path: "/v1/embeddings", body: `{"model":"text-embedding-audit","input":"hi"}`},
	}
	expectedPaths := make([]string, 0, len(requests))
	for _, test := range requests {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer sk-"+token.Key)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		// No channel is configured, so every request ends at channel selection;
		// a 401 or 404 would mean it never reached the authenticated relay chain.
		require.NotContains(t, []int{http.StatusUnauthorized, http.StatusNotFound}, recorder.Code, "%s: %s", test.path, recorder.Body.String())
		if test.audited {
			expectedPaths = append(expectedPaths, test.path)
		}
	}

	files, err := filepath.Glob(filepath.Join(auditDir, "audit-*.jsonl"))
	require.NoError(t, err)
	recordedPaths := make([]string, 0, len(expectedPaths))
	for _, name := range files {
		file, err := os.Open(name)
		require.NoError(t, err)
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			var record struct {
				Path    string `json:"path"`
				UserId  int    `json:"user_id"`
				TokenId int    `json:"token_id"`
				Status  int    `json:"status"`
			}
			require.NoError(t, common.Unmarshal(scanner.Bytes(), &record))
			recordedPaths = append(recordedPaths, record.Path)
			assert.Equal(t, user.Id, record.UserId, record.Path)
			assert.Equal(t, token.Id, record.TokenId, record.Path)
			assert.NotZero(t, record.Status, record.Path)
			assert.NotContains(t, scanner.Text(), token.Key, record.Path)
		}
		require.NoError(t, scanner.Err())
		require.NoError(t, file.Close())
	}
	slices.Sort(expectedPaths)
	slices.Sort(recordedPaths)
	assert.Equal(t, expectedPaths, recordedPaths)
}
