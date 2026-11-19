package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	KeyQuotaModeInherit = "inherit"
	KeyQuotaModeCustom  = "custom"
	KeyQuotaModeNone    = "none"
)

// KeyMeta is what an administrator records about one key of a channel.
type KeyMeta struct {
	Name      string              `json:"name,omitempty"`
	QuotaMode string              `json:"quota_mode,omitempty"`
	Quota     *dto.KeyQuotaConfig `json:"quota,omitempty"`
}

// KeyQuotaSummary aggregates the current-period key quota usage of a channel.
type KeyQuotaSummary struct {
	Enabled     bool    `json:"enabled"`
	UsedQuota   int64   `json:"used_quota"`
	LimitQuota  int64   `json:"limit_quota"`
	Percent     float64 `json:"percent"`
	PausedKeys  int     `json:"paused_keys"`
	TotalKeys   int     `json:"total_keys"`
	NextResetAt int64   `json:"next_reset_at"`
	// WarnPercent and StopPercent are the lowest lines among the keys with a
	// quota, for colouring the aggregate bar; 0 when no key has a quota.
	WarnPercent int `json:"warn_percent"`
	StopPercent int `json:"stop_percent"`
}

// ChannelKeyUsage is the usage ledger of one channel key in one quota period.
type ChannelKeyUsage struct {
	Id           int    `json:"id"`
	ChannelId    int    `json:"channel_id" gorm:"index;uniqueIndex:idx_channel_key_period,priority:1"`
	KeyHash      string `json:"key_hash" gorm:"type:varchar(16);uniqueIndex:idx_channel_key_period,priority:2"`
	PeriodStart  int64  `json:"period_start" gorm:"bigint;uniqueIndex:idx_channel_key_period,priority:3"`
	UsedQuota    int64  `json:"used_quota" gorm:"bigint;default:0"`
	RequestCount int64  `json:"request_count" gorm:"bigint;default:0"`
	WarnedAt     int64  `json:"warned_at" gorm:"bigint;default:0"`
	PausedAt     int64  `json:"paused_at" gorm:"bigint;default:0"`
	ResumedAt    int64  `json:"resumed_at" gorm:"bigint;default:0"`
	UpdatedAt    int64  `json:"updated_at" gorm:"bigint"`
}

// KeyFingerprint identifies a key without storing it: the first 16 hex
// characters of its SHA-256.
func KeyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:16]
}

// KeyPreview masks a key to its first 6 and last 4 characters.
func KeyPreview(key string) string {
	runes := []rune(key)
	if len(runes) <= 10 {
		if len(runes) <= 4 {
			return "…"
		}
		return "…" + string(runes[len(runes)-4:])
	}
	return string(runes[:6]) + "…" + string(runes[len(runes)-4:])
}

// QuotaKeys returns the keys a key quota applies to: every key of a
// multi-key channel, or the channel key.
func (channel *Channel) QuotaKeys() []string {
	if channel.ChannelInfo.IsMultiKey {
		return channel.GetKeys()
	}
	return []string{channel.Key}
}

// KeyQuotaStatus is the status of the key at index: the key status of a
// multi-key channel, or the channel status.
func (channel *Channel) KeyQuotaStatus(index int) int {
	if !channel.ChannelInfo.IsMultiKey {
		return channel.Status
	}
	if status, ok := channel.ChannelInfo.MultiKeyStatusList[index]; ok {
		return status
	}
	return common.ChannelStatusEnabled
}

// ChannelKeyQuota returns the channel-level key quota config, nil without one.
func (channel *Channel) ChannelKeyQuota() *dto.KeyQuotaConfig {
	settings := channel.GetOtherSettings()
	if settings.KeyQuota == nil {
		return nil
	}
	cfg := settings.KeyQuota.Normalized()
	return &cfg
}

// EffectiveKeyQuota returns the quota mode of the key with fingerprint and
// the enabled quota config it follows, nil when none applies.
func (channel *Channel) EffectiveKeyQuota(fingerprint string) (string, *dto.KeyQuotaConfig) {
	meta := channel.ChannelInfo.KeyMeta[fingerprint]
	if meta.QuotaMode == KeyQuotaModeCustom && meta.Quota != nil {
		if !meta.Quota.Enabled {
			return KeyQuotaModeCustom, nil
		}
		cfg := meta.Quota.Normalized()
		return KeyQuotaModeCustom, &cfg
	}
	cfg := channel.ChannelKeyQuota()
	if cfg == nil || !cfg.Enabled {
		return KeyQuotaModeNone, nil
	}
	return KeyQuotaModeInherit, cfg
}

func quotaPeriodBoundary(year int, month time.Month, cfg dto.KeyQuotaConfig, loc *time.Location) time.Time {
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
	return time.Date(year, month, min(cfg.ResetDay, lastDay), cfg.ResetHour, 0, 0, 0, loc)
}

// QuotaPeriodStart is the latest reset time of cfg not after now, in now's
// location; a reset day past the month's end falls on its last day.
func QuotaPeriodStart(cfg dto.KeyQuotaConfig, now time.Time) time.Time {
	cfg = cfg.Normalized()
	start := quotaPeriodBoundary(now.Year(), now.Month(), cfg, now.Location())
	if start.After(now) {
		start = quotaPeriodBoundary(now.Year(), now.Month()-1, cfg, now.Location())
	}
	return start
}

// QuotaNextReset is the first reset time of cfg after the period starting at
// periodStart.
func QuotaNextReset(cfg dto.KeyQuotaConfig, periodStart time.Time) time.Time {
	cfg = cfg.Normalized()
	return quotaPeriodBoundary(periodStart.Year(), periodStart.Month()+1, cfg, periodStart.Location())
}

// KeyQuotaStopReached reports usage at or over the stop line of cfg.
func KeyQuotaStopReached(cfg dto.KeyQuotaConfig, used int64) bool {
	return used*100 >= cfg.MonthlyQuota*int64(cfg.StopPercent)
}

// KeyQuotaWarnReached reports usage at or over the warn line of cfg.
func KeyQuotaWarnReached(cfg dto.KeyQuotaConfig, used int64) bool {
	return used*100 >= cfg.MonthlyQuota*int64(cfg.WarnPercent)
}

func ensureChannelKeyUsage(channelID int, keyHash string, periodStart int64) error {
	row := ChannelKeyUsage{ChannelId: channelID, KeyHash: keyHash, PeriodStart: periodStart, UpdatedAt: common.GetTimestamp()}
	return DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

// IncrChannelKeyUsage adds quota and requests to the ledger row of the key
// in the period and returns the row after the change.
func IncrChannelKeyUsage(channelID int, keyHash string, periodStart int64, quota int64, requests int64) (*ChannelKeyUsage, error) {
	if err := ensureChannelKeyUsage(channelID, keyHash, periodStart); err != nil {
		return nil, err
	}
	err := DB.Model(&ChannelKeyUsage{}).
		Where("channel_id = ? AND key_hash = ? AND period_start = ?", channelID, keyHash, periodStart).
		Updates(map[string]any{
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"request_count": gorm.Expr("request_count + ?", requests),
			"updated_at":    common.GetTimestamp(),
		}).Error
	if err != nil {
		return nil, err
	}
	return GetChannelKeyUsage(channelID, keyHash, periodStart)
}

// GetChannelKeyUsage returns the ledger row of the key in the period, a zero
// row when there is none yet.
func GetChannelKeyUsage(channelID int, keyHash string, periodStart int64) (*ChannelKeyUsage, error) {
	var row ChannelKeyUsage
	err := DB.Where("channel_id = ? AND key_hash = ? AND period_start = ?", channelID, keyHash, periodStart).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &ChannelKeyUsage{ChannelId: channelID, KeyHash: keyHash, PeriodStart: periodStart}, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// GetChannelKeyUsages returns every ledger row of the channels, by channel id.
func GetChannelKeyUsages(channelIDs []int) (map[int][]ChannelKeyUsage, error) {
	result := make(map[int][]ChannelKeyUsage)
	if len(channelIDs) == 0 {
		return result, nil
	}
	var rows []ChannelKeyUsage
	if err := DB.Where("channel_id IN ?", channelIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ChannelId] = append(result[row.ChannelId], row)
	}
	return result, nil
}

// MarkChannelKeyUsage sets a once-per-period flag (warned_at, paused_at or
// resumed_at) of the key in the period and reports whether this call set it.
func MarkChannelKeyUsage(channelID int, keyHash string, periodStart int64, column string) (bool, error) {
	switch column {
	case "warned_at", "paused_at", "resumed_at":
	default:
		return false, fmt.Errorf("unknown key usage flag %q", column)
	}
	if err := ensureChannelKeyUsage(channelID, keyHash, periodStart); err != nil {
		return false, err
	}
	result := DB.Model(&ChannelKeyUsage{}).
		Where("channel_id = ? AND key_hash = ? AND period_start = ? AND "+column+" = 0", channelID, keyHash, periodStart).
		Update(column, common.GetTimestamp())
	return result.RowsAffected > 0, result.Error
}

// ResetChannelKeyUsage zeroes the usage of the key in the period and clears
// its warned and paused flags.
func ResetChannelKeyUsage(channelID int, keyHash string, periodStart int64) error {
	if err := ensureChannelKeyUsage(channelID, keyHash, periodStart); err != nil {
		return err
	}
	return DB.Model(&ChannelKeyUsage{}).
		Where("channel_id = ? AND key_hash = ? AND period_start = ?", channelID, keyHash, periodStart).
		Updates(map[string]any{"used_quota": 0, "request_count": 0, "warned_at": 0, "paused_at": 0, "updated_at": common.GetTimestamp()}).Error
}

// PruneKeyMeta drops the meta of keys the channel no longer has.
func (channel *Channel) PruneKeyMeta() {
	if len(channel.ChannelInfo.KeyMeta) == 0 {
		return
	}
	present := make(map[string]bool)
	for _, key := range channel.QuotaKeys() {
		present[KeyFingerprint(key)] = true
	}
	for fingerprint := range channel.ChannelInfo.KeyMeta {
		if !present[fingerprint] {
			delete(channel.ChannelInfo.KeyMeta, fingerprint)
		}
	}
}

// ErrChannelKeyNotFound reports a fingerprint that matches no key of the
// channel.
var ErrChannelKeyNotFound = errors.New("channel key not found")

// SaveChannelKeyMeta stores meta for the key with fingerprint; an empty meta
// removes the entry.
func SaveChannelKeyMeta(channelID int, fingerprint string, meta KeyMeta) error {
	// channel_info also holds the polling cursor; see UpdateChannelStatus.
	pollingLock := GetChannelPollingLock(channelID)
	pollingLock.Lock()
	defer pollingLock.Unlock()
	remove := meta.Name == "" && meta.QuotaMode != KeyQuotaModeCustom
	stored, err := UpdateChannelLocked(channelID, func(channel *Channel) error {
		if !slices.ContainsFunc(channel.QuotaKeys(), func(key string) bool { return KeyFingerprint(key) == fingerprint }) {
			return ErrChannelKeyNotFound
		}
		if channel.ChannelInfo.KeyMeta == nil {
			channel.ChannelInfo.KeyMeta = make(map[string]KeyMeta)
		}
		if remove {
			delete(channel.ChannelInfo.KeyMeta, fingerprint)
		} else {
			channel.ChannelInfo.KeyMeta[fingerprint] = meta
		}
		channel.PruneKeyMeta()
		return nil
	})
	if err != nil {
		return err
	}
	if common.MemoryCacheEnabled {
		if cached, _ := CacheGetChannel(channelID); cached != nil {
			cached.ChannelInfo.KeyMeta = stored.ChannelInfo.KeyMeta
		}
	}
	return nil
}

// multiKeyUnavailableStatus is the status of a multi-key channel none of whose
// keys is enabled: quota paused while any key waits for its quota period,
// otherwise automatically disabled.
func multiKeyUnavailableStatus(statusList map[int]int) int {
	for _, status := range statusList {
		if status == common.ChannelStatusQuotaPaused {
			return common.ChannelStatusQuotaPaused
		}
	}
	return common.ChannelStatusAutoDisabled
}

// SetChannelKeyQuotaPaused pauses (status 4) the enabled key with fingerprint,
// or enables it again when it is quota paused. On a single-key channel the
// channel itself is paused or enabled. It reports whether the status changed.
func SetChannelKeyQuotaPaused(channelID int, fingerprint string, paused bool, reason string) (bool, error) {
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()
	}
	pollingLock := GetChannelPollingLock(channelID)
	pollingLock.Lock()
	defer pollingLock.Unlock()

	keyIndex, isMultiKey, newStatus, statusChanged := -1, false, 0, false
	written, err := UpdateChannelOtherInfo(channelID, func(channel *Channel) (map[string]any, error) {
		keyIndex = -1
		for i, key := range channel.QuotaKeys() {
			if KeyFingerprint(key) == fingerprint {
				keyIndex = i
				break
			}
		}
		if keyIndex < 0 {
			return nil, nil
		}
		before := channel.Status
		current := channel.KeyQuotaStatus(keyIndex)
		if paused && current != common.ChannelStatusEnabled || !paused && current != common.ChannelStatusQuotaPaused {
			return nil, nil
		}
		info := channel.GetOtherInfo()
		isMultiKey = channel.ChannelInfo.IsMultiKey
		if !isMultiKey {
			channel.Status = common.ChannelStatusEnabled
			if paused {
				channel.Status = common.ChannelStatusQuotaPaused
			}
			info["status_reason"] = reason
			info["status_time"] = common.GetTimestamp()
		} else if paused {
			ci := &channel.ChannelInfo
			if ci.MultiKeyStatusList == nil {
				ci.MultiKeyStatusList = make(map[int]int)
			}
			if ci.MultiKeyDisabledReason == nil {
				ci.MultiKeyDisabledReason = make(map[int]string)
			}
			if ci.MultiKeyDisabledTime == nil {
				ci.MultiKeyDisabledTime = make(map[int]int64)
			}
			ci.MultiKeyStatusList[keyIndex] = common.ChannelStatusQuotaPaused
			ci.MultiKeyDisabledReason[keyIndex] = reason
			ci.MultiKeyDisabledTime[keyIndex] = common.GetTimestamp()
			if channel.Status == common.ChannelStatusEnabled && !hasEnabledMultiKey(channel.GetKeys(), ci.MultiKeyStatusList) {
				channel.Status = common.ChannelStatusQuotaPaused
				info["status_reason"] = ChannelStatusReasonAllKeysDisabled
				info["status_time"] = common.GetTimestamp()
			}
		} else {
			delete(channel.ChannelInfo.MultiKeyStatusList, keyIndex)
			delete(channel.ChannelInfo.MultiKeyDisabledReason, keyIndex)
			delete(channel.ChannelInfo.MultiKeyDisabledTime, keyIndex)
			if channel.Status == common.ChannelStatusQuotaPaused || channel.Status == common.ChannelStatusAutoDisabled {
				channel.Status = common.ChannelStatusEnabled
				info["status_reason"] = ""
				info["status_time"] = common.GetTimestamp()
			}
		}
		channel.SetOtherInfo(info)
		newStatus, statusChanged = channel.Status, before != channel.Status
		updates := map[string]any{"status": channel.Status}
		if isMultiKey {
			updates["channel_info"] = channel.ChannelInfo
		}
		return updates, nil
	})
	if err != nil || !written {
		return false, err
	}
	if common.MemoryCacheEnabled {
		if cached, _ := CacheGetChannel(channelID); cached != nil && isMultiKey {
			statusList := make(map[int]int, len(cached.ChannelInfo.MultiKeyStatusList)+1)
			maps.Copy(statusList, cached.ChannelInfo.MultiKeyStatusList)
			if paused {
				statusList[keyIndex] = common.ChannelStatusQuotaPaused
			} else {
				delete(statusList, keyIndex)
			}
			cached.ChannelInfo.MultiKeyStatusList = statusList
		}
		if statusChanged {
			CacheUpdateChannelStatus(channelID, newStatus)
		}
	}
	if statusChanged {
		if err := UpdateAbilityStatus(channelID, newStatus == common.ChannelStatusEnabled); err != nil {
			common.SysLog(common.LogText("failed to update ability status: channel_id=%d, error=%v", channelID, err))
		}
	}
	BumpSyncTopic(SyncTopicChannels)
	return true, nil
}

// GetChannelKeysByIDs returns the stored key of each channel, by id.
func GetChannelKeysByIDs(ids []int) (map[int]string, error) {
	result := make(map[int]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var rows []struct {
		Id  int
		Key string
	}
	if err := DB.Model(&Channel{}).Select([]string{"id", "key"}).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.Id] = row.Key
	}
	return result, nil
}
