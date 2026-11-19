package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const keyQuotaSettings = `{"key_quota":{"enabled":true,"monthly_quota":1000,"reset_day":1,"reset_hour":0,"stop_percent":90,"warn_percent":80}}`

func setupKeyQuotaTest(t *testing.T) {
	t.Helper()
	memoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.MemoryCacheEnabled = memoryCacheEnabled
		model.DB.Exec("DELETE FROM channels")
		model.DB.Exec("DELETE FROM abilities")
		model.DB.Exec("DELETE FROM channel_key_usages")
	})
}

func createKeyQuotaChannel(t *testing.T, key string, multiKey bool) *model.Channel {
	t.Helper()
	channel := &model.Channel{
		Name:          "quota",
		Key:           key,
		Status:        common.ChannelStatusEnabled,
		OtherSettings: keyQuotaSettings,
	}
	if multiKey {
		channel.ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling}
	}
	require.NoError(t, model.DB.Create(channel).Error)
	return channel
}

func storedQuotaChannel(t *testing.T, id int) *model.Channel {
	t.Helper()
	channel, err := model.GetChannelById(id, true)
	require.NoError(t, err)
	return channel
}

func TestQuotaPeriodBoundaries(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	cases := []struct {
		name      string
		day, hour int
		now       time.Time
		start     time.Time
		next      time.Time
	}{
		{"day 31 clamps to Feb 28", 31, 0, time.Date(2026, 3, 10, 0, 0, 0, 0, loc), time.Date(2026, 2, 28, 0, 0, 0, 0, loc), time.Date(2026, 3, 31, 0, 0, 0, 0, loc)},
		{"day 31 clamps to Feb 29 in a leap year", 31, 0, time.Date(2028, 2, 29, 12, 0, 0, 0, loc), time.Date(2028, 2, 29, 0, 0, 0, 0, loc), time.Date(2028, 3, 31, 0, 0, 0, 0, loc)},
		{"before the reset hour is the previous period", 15, 9, time.Date(2026, 5, 15, 8, 59, 0, 0, loc), time.Date(2026, 4, 15, 9, 0, 0, 0, loc), time.Date(2026, 5, 15, 9, 0, 0, 0, loc)},
		{"at the reset time a new period starts", 15, 9, time.Date(2026, 5, 15, 9, 0, 0, 0, loc), time.Date(2026, 5, 15, 9, 0, 0, 0, loc), time.Date(2026, 6, 15, 9, 0, 0, 0, loc)},
		{"January wraps to December", 1, 6, time.Date(2026, 1, 1, 5, 0, 0, 0, loc), time.Date(2025, 12, 1, 6, 0, 0, 0, loc), time.Date(2026, 1, 1, 6, 0, 0, 0, loc)},
		{"day 30 in a 31-day month", 30, 0, time.Date(2026, 1, 31, 0, 0, 0, 0, loc), time.Date(2026, 1, 30, 0, 0, 0, 0, loc), time.Date(2026, 2, 28, 0, 0, 0, 0, loc)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := kitdto.KeyQuotaConfig{Enabled: true, MonthlyQuota: 1, ResetDay: tc.day, ResetHour: tc.hour}
			start := model.QuotaPeriodStart(cfg, tc.now)
			assert.True(t, tc.start.Equal(start), "start %s", start)
			assert.True(t, tc.next.Equal(model.QuotaNextReset(cfg, start)), "next %s", model.QuotaNextReset(cfg, start))
		})
	}
}

func TestKeyQuotaConfigValidate(t *testing.T) {
	valid := kitdto.KeyQuotaConfig{Enabled: true, MonthlyQuota: 100, ResetDay: 31, ResetHour: 23}
	require.NoError(t, valid.Validate())
	for _, invalid := range []kitdto.KeyQuotaConfig{
		{Enabled: true, MonthlyQuota: 0},
		{Enabled: true, MonthlyQuota: 1, ResetDay: 32},
		{Enabled: true, MonthlyQuota: 1, ResetHour: 24},
		{Enabled: true, MonthlyQuota: 1, WarnPercent: 90, StopPercent: 90},
		{Enabled: true, MonthlyQuota: 1, StopPercent: 101},
	} {
		assert.Error(t, invalid.Validate(), "%+v", invalid)
	}
	assert.NoError(t, (&kitdto.KeyQuotaConfig{Enabled: false, ResetDay: 99}).Validate())
}

func TestRecordChannelKeyUsageWarnsAndPausesSingleKey(t *testing.T) {
	setupKeyQuotaTest(t)
	channel := createKeyQuotaChannel(t, "sk-single-key-0001", false)
	fingerprint := model.KeyFingerprint(channel.Key)

	RecordChannelKeyUsage(channel.Id, channel.Key, 850, 1)
	stored := storedQuotaChannel(t, channel.Id)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	_, cfg := stored.EffectiveKeyQuota(fingerprint)
	require.NotNil(t, cfg)
	periodStart := model.QuotaPeriodStart(*cfg, time.Now()).Unix()
	usage, err := model.GetChannelKeyUsage(channel.Id, fingerprint, periodStart)
	require.NoError(t, err)
	assert.EqualValues(t, 850, usage.UsedQuota)
	assert.EqualValues(t, 1, usage.RequestCount)
	assert.NotZero(t, usage.WarnedAt)
	assert.Zero(t, usage.PausedAt)

	RecordChannelKeyUsage(channel.Id, channel.Key, 50, 1)
	stored = storedQuotaChannel(t, channel.Id)
	assert.Equal(t, common.ChannelStatusQuotaPaused, stored.Status)
	assert.Contains(t, stored.GetOtherInfo()["status_reason"], "额度暂停")
	usage, err = model.GetChannelKeyUsage(channel.Id, fingerprint, periodStart)
	require.NoError(t, err)
	assert.EqualValues(t, 900, usage.UsedQuota)
	assert.NotZero(t, usage.PausedAt)

	// The channel test never re-enables a quota-paused channel.
	automaticEnable := common.AutomaticEnableChannelEnabled
	common.AutomaticEnableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticEnableChannelEnabled = automaticEnable })
	assert.False(t, ShouldEnableChannel(nil, common.ChannelStatusQuotaPaused))
}

func TestRecordChannelKeyUsagePausesMultiKeyThenChannel(t *testing.T) {
	setupKeyQuotaTest(t)
	channel := createKeyQuotaChannel(t, "sk-multi-key-aaaa\nsk-multi-key-bbbb", true)

	RecordChannelKeyUsage(channel.Id, "sk-multi-key-bbbb", 950, 1)
	stored := storedQuotaChannel(t, channel.Id)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Equal(t, common.ChannelStatusQuotaPaused, stored.ChannelInfo.MultiKeyStatusList[1])
	key, index, apiErr := stored.GetNextEnabledKey()
	require.Nil(t, apiErr)
	assert.Equal(t, "sk-multi-key-aaaa", key)
	assert.Equal(t, 0, index)

	RecordChannelKeyUsage(channel.Id, "sk-multi-key-aaaa", 900, 1)
	stored = storedQuotaChannel(t, channel.Id)
	assert.Equal(t, common.ChannelStatusQuotaPaused, stored.Status)
	assert.Equal(t, common.ChannelStatusQuotaPaused, stored.ChannelInfo.MultiKeyStatusList[0])
}

func TestRecordChannelKeyUsageFailsClosed(t *testing.T) {
	setupKeyQuotaTest(t)
	channel := createKeyQuotaChannel(t, "sk-fail-closed-0001", false)
	require.NoError(t, model.DB.Migrator().DropTable(&model.ChannelKeyUsage{}))
	t.Cleanup(func() { require.NoError(t, model.DB.AutoMigrate(&model.ChannelKeyUsage{})) })

	RecordChannelKeyUsage(channel.Id, channel.Key, 1, 1)
	assert.Equal(t, common.ChannelStatusQuotaPaused, storedQuotaChannel(t, channel.Id).Status)
}

func TestResumeChannelKeysAfterReset(t *testing.T) {
	setupKeyQuotaTest(t)
	channel := createKeyQuotaChannel(t, "sk-resume-key-aaaa\nsk-resume-key-bbbb", true)
	RecordChannelKeyUsage(channel.Id, "sk-resume-key-aaaa", 950, 1)
	RecordChannelKeyUsage(channel.Id, "sk-resume-key-bbbb", 950, 1)
	require.Equal(t, common.ChannelStatusQuotaPaused, storedQuotaChannel(t, channel.Id).Status)

	// Still within the period: usage is over the stop line, nothing resumes.
	require.NoError(t, ResumeChannelKeysBelowQuota(time.Now()))
	assert.Equal(t, common.ChannelStatusQuotaPaused, storedQuotaChannel(t, channel.Id).Status)

	cfg := *storedQuotaChannel(t, channel.Id).ChannelKeyQuota()
	afterReset := model.QuotaNextReset(cfg, model.QuotaPeriodStart(cfg, time.Now())).Add(time.Minute)
	require.NoError(t, ResumeChannelKeysBelowQuota(afterReset))
	stored := storedQuotaChannel(t, channel.Id)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Empty(t, stored.ChannelInfo.MultiKeyStatusList)
	usage, err := model.GetChannelKeyUsage(channel.Id, model.KeyFingerprint("sk-resume-key-aaaa"), model.QuotaPeriodStart(cfg, afterReset).Unix())
	require.NoError(t, err)
	assert.Zero(t, usage.UsedQuota)
	assert.NotZero(t, usage.ResumedAt)
}

func TestResumeChannelPausedKeysResumesAll(t *testing.T) {
	setupKeyQuotaTest(t)
	channel := createKeyQuotaChannel(t, "sk-resume-all-aaaa\nsk-resume-all-bbbb", true)
	RecordChannelKeyUsage(channel.Id, "sk-resume-all-aaaa", 950, 1)
	RecordChannelKeyUsage(channel.Id, "sk-resume-all-bbbb", 950, 1)
	stored := storedQuotaChannel(t, channel.Id)
	require.Equal(t, common.ChannelStatusQuotaPaused, stored.Status)

	resumed, err := ResumeChannelPausedKeys(stored)
	require.NoError(t, err)
	assert.Equal(t, 2, resumed)
	stored = storedQuotaChannel(t, channel.Id)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Empty(t, stored.ChannelInfo.MultiKeyStatusList)
}

func TestResumeLeavesManuallyDisabledChannel(t *testing.T) {
	setupKeyQuotaTest(t)
	channel := createKeyQuotaChannel(t, "sk-manual-0001", false)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("status", common.ChannelStatusManuallyDisabled).Error)

	RecordChannelKeyUsage(channel.Id, channel.Key, 950, 1)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, storedQuotaChannel(t, channel.Id).Status)
	require.NoError(t, ResumeChannelKeysBelowQuota(time.Now().AddDate(0, 2, 0)))
	assert.Equal(t, common.ChannelStatusManuallyDisabled, storedQuotaChannel(t, channel.Id).Status)
}

func TestKeyMetaFollowsFingerprintAcrossKeyEdits(t *testing.T) {
	setupKeyQuotaTest(t)
	channel := createKeyQuotaChannel(t, "sk-meta-key-aaaa\nsk-meta-key-bbbb\nsk-meta-key-cccc", true)
	custom := &kitdto.KeyQuotaConfig{Enabled: true, MonthlyQuota: 5000, ResetDay: 10}
	require.NoError(t, model.SaveChannelKeyMeta(channel.Id, model.KeyFingerprint("sk-meta-key-bbbb"), model.KeyMeta{Name: "B", QuotaMode: model.KeyQuotaModeInherit}))
	require.NoError(t, model.SaveChannelKeyMeta(channel.Id, model.KeyFingerprint("sk-meta-key-cccc"), model.KeyMeta{Name: "C", QuotaMode: model.KeyQuotaModeCustom, Quota: custom}))

	// Delete key b and move key c first.
	_, err := model.UpdateChannelLocked(channel.Id, func(stored *model.Channel) error {
		stored.Key = "sk-meta-key-cccc\nsk-meta-key-aaaa"
		return nil
	})
	require.NoError(t, err)

	stored := storedQuotaChannel(t, channel.Id)
	assert.NotContains(t, stored.ChannelInfo.KeyMeta, model.KeyFingerprint("sk-meta-key-bbbb"))
	rows := BuildChannelKeyRows(stored, nil, time.Now())
	require.Len(t, rows, 2)
	assert.Equal(t, "C", rows[0].Name)
	assert.Equal(t, model.KeyQuotaModeCustom, rows[0].QuotaMode)
	require.NotNil(t, rows[0].EffectiveQuota)
	assert.EqualValues(t, 5000, rows[0].EffectiveQuota.MonthlyQuota)
	assert.Equal(t, "sk-met…cccc", rows[0].Preview)
	assert.Equal(t, "", rows[1].Name)
	assert.Equal(t, model.KeyQuotaModeInherit, rows[1].QuotaMode)

	keys, err := model.GetChannelKeysByIDs([]int{channel.Id})
	require.NoError(t, err)
	assert.Equal(t, "sk-meta-key-cccc\nsk-meta-key-aaaa", keys[channel.Id])
	summary := BuildKeyQuotaSummary(rows)
	assert.True(t, summary.Enabled)
	assert.Equal(t, 2, summary.TotalKeys)
	assert.EqualValues(t, 6000, summary.LimitQuota)

	assert.ErrorIs(t, model.SaveChannelKeyMeta(channel.Id, model.KeyFingerprint("sk-meta-key-bbbb"), model.KeyMeta{Name: "gone"}), model.ErrChannelKeyNotFound)
}
