package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedAbilities resets the abilities table and inserts the given (group, model,
// channel, priority) rows.
func seedAbilities(t *testing.T, rows []Ability) {
	t.Helper()

	require.NoError(t, DB.AutoMigrate(&Channel{}, &Ability{}))
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
		require.NoError(t, DB.Exec("DELETE FROM channels").Error)
	})

	for i := range rows {
		row := rows[i]
		require.NoError(t, DB.Create(&Channel{
			Id:       row.ChannelId,
			Status:   common.ChannelStatusEnabled,
			Name:     row.Group,
			Group:    row.Group,
			Models:   row.Model,
			Priority: row.Priority,
		}).Error)
		require.NoError(t, DB.Create(&row).Error)
	}
}

func abilityPriority(v int64) *int64 { return &v }

// TestGetChannelCandidateCount covers the contract the auto-group walk in
// service.CacheGetRandomSatisfiedChannel depends on: the reported count is how
// many channels serve the group and model, which bounds how many attempts the
// group can absorb before it can only re-dial one that already failed. Reporting
// distinct priority levels instead would drop channels that share a level, and
// with them the redundancy of running two upstreams at the same priority.
func TestGetChannelCandidateCount(t *testing.T) {
	prevMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = prevMemoryCache })

	seedAbilities(t, []Ability{
		{Group: "chat", Model: "gpt-test", ChannelId: 1, Enabled: true, Priority: abilityPriority(100)},
		{Group: "chat", Model: "gpt-test", ChannelId: 2, Enabled: true, Priority: abilityPriority(100)},
		{Group: "chat", Model: "gpt-test", ChannelId: 3, Enabled: true, Priority: abilityPriority(80)},
		{Group: "chat", Model: "gpt-test", ChannelId: 4, Enabled: true, Priority: abilityPriority(10)},
		{Group: "speedrun", Model: "gpt-test", ChannelId: 16, Enabled: true, Priority: abilityPriority(100)},
		{Group: "chat", Model: "gpt-test", ChannelId: 9, Enabled: false, Priority: abilityPriority(1)},
	})

	tests := []struct {
		name      string
		group     string
		retry     int
		wantCount int
		wantIn    []int
	}{
		{name: "counts channels not levels", group: "chat", retry: 0, wantCount: 4, wantIn: []int{1, 2}},
		{name: "second step descends one level", group: "chat", retry: 1, wantCount: 4, wantIn: []int{3}},
		{name: "third step reaches the lowest level", group: "chat", retry: 2, wantCount: 4, wantIn: []int{4}},
		{name: "step past the ladder clamps to the lowest level", group: "chat", retry: 5, wantCount: 4, wantIn: []int{4}},
		{name: "single channel group reports one", group: "speedrun", retry: 0, wantCount: 1, wantIn: []int{16}},
		{name: "disabled ability is not counted", group: "speedrun", retry: 3, wantCount: 1, wantIn: []int{16}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel, candidateCount, err := GetChannel(tt.group, "gpt-test", tt.retry, "")
			require.NoError(t, err)
			require.NotNil(t, channel)
			assert.Equal(t, tt.wantCount, candidateCount)
			assert.Contains(t, tt.wantIn, channel.Id)
		})
	}
}

// TestGetChannelUnservedModelIsNotAnError pins that a group which simply does not
// carry the requested model is an ordinary empty result. Reporting an error here
// surfaces a database-corruption message to clients for the very common case of
// an unknown model name.
func TestGetChannelUnservedModelIsNotAnError(t *testing.T) {
	prevMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = prevMemoryCache })

	seedAbilities(t, []Ability{
		{Group: "chat", Model: "gpt-test", ChannelId: 1, Enabled: true, Priority: abilityPriority(100)},
	})

	tests := []struct {
		name  string
		group string
		model string
	}{
		{name: "unknown model in an existing group", group: "chat", model: "not-served"},
		{name: "unknown group", group: "no-such-group", model: "gpt-test"},
	}

	for _, tt := range tests {
		for _, retry := range []int{0, 1, 4} {
			t.Run(tt.name, func(t *testing.T) {
				channel, candidateCount, err := GetChannel(tt.group, tt.model, retry, "")
				assert.NoError(t, err)
				assert.Nil(t, channel)
				assert.Zero(t, candidateCount)
			})
		}
	}
}
