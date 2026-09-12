package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retryBoundChannel describes one channel to seed for the retry-bound tests.
type retryBoundChannel struct {
	id       int
	group    string
	priority int64
}

// seedRetryBoundRouting installs channels/abilities in the database, rebuilds the
// channel cache, and points the auto-group settings at the given group order.
// The group walk itself consumes only the channel and the candidate count that
// GetRandomSatisfiedChannel reports, so it behaves identically on both selection
// paths; the SQL path's own contract is covered by TestGetChannelCandidateCount in
// package model, which is where commonGroupCol is initialised.
// 选路走查只消费 GetRandomSatisfiedChannel 返回的渠道与候选计数，因此缓存路径与 SQL
// 路径行为一致；SQL 路径自身的契约由 model 包的 TestGetChannelCandidateCount 覆盖。
func seedRetryBoundRouting(t *testing.T, modelName string, groupOrder []string, channels []retryBoundChannel) {
	t.Helper()

	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}))
	require.NoError(t, model.DB.Exec("DELETE FROM channels").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM abilities").Error)

	prevMemoryCache := common.MemoryCacheEnabled
	prevRetryTimes := common.RetryTimes
	prevAutoGroups := setting.AutoGroups2JsonString()
	prevUsableGroups := setting.UserUsableGroups2JSONString()
	prevGroupRatio := ratio_setting.GroupRatio2JSONString()
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = prevMemoryCache
		common.RetryTimes = prevRetryTimes
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(prevAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(prevUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(prevGroupRatio))
		require.NoError(t, model.DB.Exec("DELETE FROM channels").Error)
		require.NoError(t, model.DB.Exec("DELETE FROM abilities").Error)
		model.InitChannelCache()
	})

	// A group only counts as selectable once it is usable, listed in Auto, and
	// carries a group ratio.
	// 一个分组要可选，必须同时可用、在 Auto 列表里、并且配了分组倍率。
	usable := make(map[string]string, len(groupOrder))
	ratios := make(map[string]float64, len(groupOrder))
	for _, g := range groupOrder {
		usable[g] = g
		ratios[g] = 1
	}
	usableJSON, err := common.Marshal(usable)
	require.NoError(t, err)
	orderJSON, err := common.Marshal(groupOrder)
	require.NoError(t, err)
	ratiosJSON, err := common.Marshal(ratios)
	require.NoError(t, err)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(string(usableJSON)))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(string(orderJSON)))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(string(ratiosJSON)))

	for _, c := range channels {
		priority := c.priority
		require.NoError(t, model.DB.Create(&model.Channel{
			Id:       c.id,
			Status:   common.ChannelStatusEnabled,
			Name:     c.group,
			Group:    c.group,
			Models:   modelName,
			Priority: &priority,
		}).Error)
		require.NoError(t, model.DB.Create(&model.Ability{
			Group:     c.group,
			Model:     modelName,
			ChannelId: c.id,
			Enabled:   true,
			Priority:  &priority,
		}).Error)
	}
	model.InitChannelCache()
}

// replayRetryBoundAttempts reproduces the real request path: middleware/distributor.go
// performs the initial selection with its own throwaway RetryParam, then
// controller/relay.go drives the retry loop with a second, independent
// RetryParam. Every attempt is treated as a retryable failure. It returns the
// channel ids actually dialled, in order — the same thing the use_channel log
// field records in production.
// 复刻真实请求路径：distributor 用一个用完即弃的 RetryParam 做首次选路，relay 再用另一个
// 独立的 RetryParam 驱动重试循环；每次尝试都视为可重试失败。返回实际拨打的渠道 id 序列。
func replayRetryBoundAttempts(t *testing.T, modelName string) []int {
	t.Helper()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, true)

	attempts := make([]int, 0, 16)

	// middleware/distributor.go: initial selection, param discarded afterwards.
	channel, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx:         c,
		ModelName:   modelName,
		TokenGroup:  "auto",
		RequestPath: c.Request.URL.Path,
		Retry:       common.GetPointer(0),
	})
	require.NoError(t, err)
	require.NotNil(t, channel, "distributor must find an initial channel")
	attempts = append(attempts, channel.Id)

	// controller/relay.go: fresh RetryParam, retry loop. Iteration 0 reuses the
	// channel the distributor already picked, so re-selection starts at retry 1.
	retryParam := &RetryParam{
		Ctx:         c,
		ModelName:   modelName,
		TokenGroup:  "auto",
		RequestPath: c.Request.URL.Path,
		Retry:       common.GetPointer(0),
	}
	// Only the very first iteration reuses it: getChannel re-selects as soon as
	// RelayInfo.ChannelMeta is set, which does not depend on the retry counter —
	// the counter is reset to 0 again on every group switch.
	firstIteration := true
	for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
		if firstIteration {
			firstIteration = false
			continue
		}
		next, _, selErr := CacheGetRandomSatisfiedChannel(retryParam)
		if selErr != nil || next == nil {
			break
		}
		attempts = append(attempts, next.Id)
		require.Less(t, len(attempts), 64, "selection did not terminate")
	}
	return attempts
}

// TestAutoGroupCrossGroupRetryWalksGroups pins the routing sequence a client
// request actually produces when every upstream attempt fails. Each case is a
// real misrouting the clamp allows: retries stranded on one channel, a second
// group entered below its top priority, and cross-group failover silently
// skipped.
func TestAutoGroupCrossGroupRetryWalksGroups(t *testing.T) {
	const modelName = "gpt-test"

	tests := []struct {
		name       string
		retryTimes int
		groupOrder []string
		channels   []retryBoundChannel
		want       []int
	}{
		{
			// Every group holds a single channel. Retries clamp back to the same
			// priority and re-dial the same channel RetryTimes+1 times per group.
			name:       "single priority per group advances on every failure",
			retryTimes: 2,
			groupOrder: []string{"speedrun", "chat", "media"},
			channels: []retryBoundChannel{
				{id: 16, group: "speedrun", priority: 100},
				{id: 8, group: "chat", priority: 100},
				{id: 5, group: "media", priority: 100},
			},
			want: []int{16, 8, 5},
		},
		{
			// The second group must be entered at its highest priority. Channel 8
			// (priority 100) must not be skipped in favour of channel 12 (priority 10).
			name:       "second group starts at its highest priority",
			retryTimes: 2,
			groupOrder: []string{"speedrun", "chat"},
			channels: []retryBoundChannel{
				{id: 16, group: "speedrun", priority: 100},
				{id: 8, group: "chat", priority: 100},
				{id: 12, group: "chat", priority: 10},
			},
			want: []int{16, 8, 12},
		},
		{
			// A group holding more priority levels than RetryTimes+1 must not swallow
			// the whole request: the remaining groups still have to be tried.
			name:       "deep first group still yields to later groups",
			retryTimes: 2,
			groupOrder: []string{"speedrun", "chat", "media"},
			channels: []retryBoundChannel{
				{id: 1, group: "speedrun", priority: 100},
				{id: 2, group: "speedrun", priority: 80},
				{id: 3, group: "speedrun", priority: 60},
				{id: 4, group: "speedrun", priority: 40},
				{id: 8, group: "chat", priority: 100},
				{id: 5, group: "media", priority: 100},
			},
			want: []int{1, 2, 3, 8, 5},
		},
		{
			// A group that does not serve the model at all is skipped without
			// consuming an attempt. chat is then the only group carrying the model,
			// so it keeps the full retry budget rather than being cut short.
			name:       "group without the model is skipped",
			retryTimes: 2,
			groupOrder: []string{"speedrun", "chat"},
			channels: []retryBoundChannel{
				{id: 8, group: "chat", priority: 100},
				{id: 12, group: "chat", priority: 10},
			},
			want: []int{8, 12, 12},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seedRetryBoundRouting(t, modelName, tt.groupOrder, tt.channels)
			common.RetryTimes = tt.retryTimes
			assert.Equal(t, tt.want, replayRetryBoundAttempts(t, modelName))
		})
	}
}

// TestAutoGroupRetryNeverRedialsExhaustedGroup asserts the property the clamp
// violates: no channel is dialled twice in a row while another group still holds
// an untried channel, however large the retry budget is.
func TestAutoGroupRetryNeverRedialsExhaustedGroup(t *testing.T) {
	const modelName = "gpt-test"

	seedRetryBoundRouting(t, modelName, []string{"speedrun", "chat", "media"}, []retryBoundChannel{
		{id: 16, group: "speedrun", priority: 100},
		{id: 8, group: "chat", priority: 100},
		{id: 5, group: "media", priority: 100},
	})
	common.RetryTimes = 7

	attempts := replayRetryBoundAttempts(t, modelName)
	for i := 1; i < len(attempts); i++ {
		assert.NotEqual(t, attempts[i-1], attempts[i],
			"attempt %d re-dialled channel %d while other groups were untried: %v", i, attempts[i], attempts)
	}
	assert.Equal(t, []int{16, 8, 5}, attempts)
}

// TestAutoGroupSamePriorityChannelsGetSeparateAttempts pins that a group's budget
// follows its channel count, not its number of priority levels. Two channels at
// priority 100 must let the group absorb two attempts before the walk moves on;
// counting levels instead would abandon the group after a single failure and
// silently halve the redundancy the operator configured by running two upstreams
// side by side. Which of the two siblings each attempt lands on is weighted
// random, so only the attempt count is asserted here.
func TestAutoGroupSamePriorityChannelsGetSeparateAttempts(t *testing.T) {
	const modelName = "gpt-test"

	seedRetryBoundRouting(t, modelName, []string{"chat", "media"}, []retryBoundChannel{
		{id: 1, group: "chat", priority: 100},
		{id: 2, group: "chat", priority: 100},
		{id: 5, group: "media", priority: 100},
	})
	common.RetryTimes = 3

	attempts := replayRetryBoundAttempts(t, modelName)

	require.Len(t, attempts, 3, "chat must absorb one attempt per channel before media: %v", attempts)
	assert.Contains(t, []int{1, 2}, attempts[0])
	assert.Contains(t, []int{1, 2}, attempts[1])
	assert.Equal(t, 5, attempts[2], "media must only be reached after chat is used up: %v", attempts)
}

// TestAutoGroupSingleChannelModelKeepsItsRetries pins that capping a group's
// attempts never costs retries outright. A model carried by exactly one channel
// in one group has nowhere to fail over to, so the cap must not apply: the
// request keeps the RetryTimes budget the operator configured. Most models on a
// small deployment look like this.
func TestAutoGroupSingleChannelModelKeepsItsRetries(t *testing.T) {
	const modelName = "gpt-test"

	seedRetryBoundRouting(t, modelName, []string{"speedrun", "chat", "media"}, []retryBoundChannel{
		{id: 3, group: "chat", priority: 100},
	})
	common.RetryTimes = 2

	assert.Equal(t, []int{3, 3, 3}, replayRetryBoundAttempts(t, modelName),
		"a model with no alternative group must keep retrying its only channel")
}
