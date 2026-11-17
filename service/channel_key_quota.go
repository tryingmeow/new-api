package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/bytedance/gopkg/util/gopool"
)

const channelKeyQuotaResumeInterval = time.Minute

var (
	channelKeyQuotaResumeOnce    sync.Once
	channelKeyQuotaResumeRunning atomic.Bool
)

// ChannelKeyRow is one key of a channel with its quota state in the current
// period, as the key management API returns it.
type ChannelKeyRow struct {
	Index          int                    `json:"index"`
	Fingerprint    string                 `json:"fingerprint"`
	Preview        string                 `json:"preview"`
	Name           string                 `json:"name"`
	Status         int                    `json:"status"`
	DisabledReason string                 `json:"disabled_reason"`
	QuotaMode      string                 `json:"quota_mode"`
	EffectiveQuota *kitdto.KeyQuotaConfig `json:"effective_quota"`
	UsedQuota      int64                  `json:"used_quota"`
	RequestCount   int64                  `json:"request_count"`
	PeriodStart    int64                  `json:"period_start"`
	NextResetAt    int64                  `json:"next_reset_at"`
	PausedAt       int64                  `json:"paused_at"`
	Percent        *float64               `json:"percent"`
}

func keyQuotaPercent(cfg kitdto.KeyQuotaConfig, used int64) float64 {
	if cfg.MonthlyQuota <= 0 {
		return 0
	}
	return min(max(float64(used)*100/float64(cfg.MonthlyQuota), 0), 100)
}

func findKeyUsage(rows []model.ChannelKeyUsage, fingerprint string, periodStart int64) model.ChannelKeyUsage {
	for _, row := range rows {
		if row.KeyHash == fingerprint && row.PeriodStart == periodStart {
			return row
		}
	}
	return model.ChannelKeyUsage{KeyHash: fingerprint, PeriodStart: periodStart}
}

// BuildChannelKeyRows lists the keys of channel, which must hold its key,
// with their quota state at now, reading usage from rows.
func BuildChannelKeyRows(channel *model.Channel, rows []model.ChannelKeyUsage, now time.Time) []ChannelKeyRow {
	keys := channel.QuotaKeys()
	result := make([]ChannelKeyRow, 0, len(keys))
	for i, key := range keys {
		fingerprint := model.KeyFingerprint(key)
		mode, cfg := channel.EffectiveKeyQuota(fingerprint)
		row := ChannelKeyRow{
			Index:          i,
			Fingerprint:    fingerprint,
			Preview:        model.KeyPreview(key),
			Name:           channel.ChannelInfo.KeyMeta[fingerprint].Name,
			Status:         channel.KeyQuotaStatus(i),
			QuotaMode:      mode,
			EffectiveQuota: cfg,
		}
		if channel.ChannelInfo.IsMultiKey {
			row.DisabledReason = channel.ChannelInfo.MultiKeyDisabledReason[i]
		} else if row.Status != common.ChannelStatusEnabled {
			row.DisabledReason, _ = channel.GetOtherInfo()["status_reason"].(string)
		}
		if row.Status == common.ChannelStatusEnabled {
			row.DisabledReason = ""
		}
		if cfg != nil {
			start := model.QuotaPeriodStart(*cfg, now)
			usage := findKeyUsage(rows, fingerprint, start.Unix())
			percent := keyQuotaPercent(*cfg, usage.UsedQuota)
			row.UsedQuota, row.RequestCount, row.PausedAt = usage.UsedQuota, usage.RequestCount, usage.PausedAt
			row.PeriodStart, row.NextResetAt = start.Unix(), model.QuotaNextReset(*cfg, start).Unix()
			row.Percent = &percent
		}
		result = append(result, row)
	}
	return result
}

// BuildKeyQuotaSummary aggregates the key rows of a channel for the channel
// list.
func BuildKeyQuotaSummary(keyRows []ChannelKeyRow) *model.KeyQuotaSummary {
	summary := &model.KeyQuotaSummary{}
	for _, row := range keyRows {
		if row.Status == common.ChannelStatusQuotaPaused {
			summary.PausedKeys++
		}
		if row.EffectiveQuota == nil {
			continue
		}
		summary.Enabled = true
		summary.TotalKeys++
		summary.UsedQuota += row.UsedQuota
		summary.LimitQuota += row.EffectiveQuota.MonthlyQuota
		if summary.WarnPercent == 0 || row.EffectiveQuota.WarnPercent < summary.WarnPercent {
			summary.WarnPercent = row.EffectiveQuota.WarnPercent
		}
		if summary.StopPercent == 0 || row.EffectiveQuota.StopPercent < summary.StopPercent {
			summary.StopPercent = row.EffectiveQuota.StopPercent
		}
		if summary.NextResetAt == 0 || row.NextResetAt < summary.NextResetAt {
			summary.NextResetAt = row.NextResetAt
		}
	}
	if summary.LimitQuota > 0 {
		summary.Percent = keyQuotaPercent(kitdto.KeyQuotaConfig{MonthlyQuota: summary.LimitQuota}, summary.UsedQuota)
	}
	return summary
}

func keyQuotaNotifyType(channelID int, fingerprint string, event string) string {
	return fmt.Sprintf("%s_%d_key_%s_%s", kitdto.NotifyTypeChannelUpdate, channelID, fingerprint, event)
}

func keyQuotaLabel(channel *model.Channel, key string) string {
	if name := channel.ChannelInfo.KeyMeta[model.KeyFingerprint(key)].Name; name != "" {
		return name
	}
	return model.KeyPreview(key)
}

func notifyKeyQuota(channel *model.Channel, key string, event string, subject string, content string, params map[string]any) {
	params["Name"], params["Id"], params["Key"] = channel.Name, channel.Id, keyQuotaLabel(channel, key)
	NotifyChannelWatchers(keyQuotaNotifyType(channel.Id, model.KeyFingerprint(key), event), func(lang string) (string, string) {
		return i18n.Translate(lang, subject, params), i18n.Translate(lang, content, params)
	})
}

func keyQuotaPauseReason(cfg kitdto.KeyQuotaConfig, used int64) string {
	return fmt.Sprintf("额度暂停: 本月已用 %s / 上限 %s", logger.FormatQuota(int(min(used, int64(common.MaxQuota)))), logger.FormatQuota(int(min(cfg.MonthlyQuota, int64(common.MaxQuota)))))
}

// RecordChannelKeyUsage adds a settled quota (negative for a refund) and
// requests to the ledger of the channel key that served them, and pauses the
// key at its stop line. A key without an enabled quota is not counted. When
// the ledger cannot be written the key is paused, failing closed.
func RecordChannelKeyUsage(channelID int, key string, quota int, requests int) {
	if channelID <= 0 || key == "" || quota == 0 && requests == 0 {
		return
	}
	channel, err := model.CacheGetChannel(channelID)
	if err != nil || channel == nil {
		return
	}
	fingerprint := model.KeyFingerprint(key)
	_, cfg := channel.EffectiveKeyQuota(fingerprint)
	if cfg == nil {
		return
	}
	periodStart := model.QuotaPeriodStart(*cfg, time.Now()).Unix()
	usage, err := model.IncrChannelKeyUsage(channelID, fingerprint, periodStart, int64(quota), int64(requests))
	if err != nil {
		common.SysError(common.LogText("failed to record channel key usage, pausing the key: channel_id=%d, key=%s, error=%v", channelID, fingerprint, err))
		reason := "额度暂停: 用量记录失败"
		if changed, pauseErr := model.SetChannelKeyQuotaPaused(channelID, fingerprint, true, reason); pauseErr != nil {
			common.SysError(common.LogText("failed to pause channel key: channel_id=%d, key=%s, error=%v", channelID, fingerprint, pauseErr))
		} else if changed {
			notifyKeyQuota(channel, key, "paused", i18n.MsgChannelKeyQuotaPausedSubject, i18n.MsgChannelKeyQuotaPausedContent, map[string]any{"Reason": reason})
		}
		return
	}
	checkChannelKeyQuota(channel, key, *cfg, usage)
}

// checkChannelKeyQuota sends the warning and pauses the key once its usage
// reaches the respective line.
func checkChannelKeyQuota(channel *model.Channel, key string, cfg kitdto.KeyQuotaConfig, usage *model.ChannelKeyUsage) {
	fingerprint := usage.KeyHash
	if model.KeyQuotaWarnReached(cfg, usage.UsedQuota) && usage.WarnedAt == 0 {
		if marked, err := model.MarkChannelKeyUsage(channel.Id, fingerprint, usage.PeriodStart, "warned_at"); err == nil && marked {
			notifyKeyQuota(channel, key, "warn", i18n.MsgChannelKeyQuotaWarnSubject, i18n.MsgChannelKeyQuotaWarnContent, map[string]any{
				"Used":    logger.FormatQuota(int(min(usage.UsedQuota, int64(common.MaxQuota)))),
				"Limit":   logger.FormatQuota(int(min(cfg.MonthlyQuota, int64(common.MaxQuota)))),
				"Percent": fmt.Sprintf("%.1f", keyQuotaPercent(cfg, usage.UsedQuota)),
			})
		}
	}
	if !model.KeyQuotaStopReached(cfg, usage.UsedQuota) {
		return
	}
	reason := keyQuotaPauseReason(cfg, usage.UsedQuota)
	changed, err := model.SetChannelKeyQuotaPaused(channel.Id, fingerprint, true, reason)
	if err != nil {
		common.SysError(common.LogText("failed to pause channel key: channel_id=%d, key=%s, error=%v", channel.Id, fingerprint, err))
		return
	}
	if !changed {
		return
	}
	common.SysLog(common.LogText("channel key paused by its quota: channel_id=%d, key=%s", channel.Id, fingerprint))
	if marked, err := model.MarkChannelKeyUsage(channel.Id, fingerprint, usage.PeriodStart, "paused_at"); err == nil && marked {
		notifyKeyQuota(channel, key, "paused", i18n.MsgChannelKeyQuotaPausedSubject, i18n.MsgChannelKeyQuotaPausedContent, map[string]any{"Reason": reason})
	}
}

// RecordTaskChannelKeyUsage adjusts the key ledger for a later settlement of
// an async task. The key is the one the task kept, or the channel key of a
// single-key channel; a multi-key channel task without its key is skipped.
func RecordTaskChannelKeyUsage(task *model.Task, quota int) {
	if task == nil || quota == 0 {
		return
	}
	key := task.PrivateData.Key
	if key == "" {
		channel, err := model.CacheGetChannel(task.ChannelId)
		if err != nil || channel == nil || channel.ChannelInfo.IsMultiKey {
			return
		}
		key = channel.Key
	}
	RecordChannelKeyUsage(task.ChannelId, key, quota, 0)
}

// ResumeChannelKey enables a quota-paused key again now and records the
// resume for the current period. It reports whether the key was paused.
func ResumeChannelKey(channel *model.Channel, key string) (bool, error) {
	fingerprint := model.KeyFingerprint(key)
	changed, err := model.SetChannelKeyQuotaPaused(channel.Id, fingerprint, false, "")
	if err != nil || !changed {
		return changed, err
	}
	if _, cfg := channel.EffectiveKeyQuota(fingerprint); cfg != nil {
		periodStart := model.QuotaPeriodStart(*cfg, time.Now()).Unix()
		if _, err := model.MarkChannelKeyUsage(channel.Id, fingerprint, periodStart, "resumed_at"); err != nil {
			common.SysError(common.LogText("failed to record channel key resume: channel_id=%d, key=%s, error=%v", channel.Id, fingerprint, err))
		}
	}
	return true, nil
}

// ResumeChannelPausedKeys enables every quota-paused key of one channel now,
// keeping usage. It returns how many keys were resumed.
func ResumeChannelPausedKeys(channel *model.Channel) (int, error) {
	resumed := 0
	for i, key := range channel.QuotaKeys() {
		if channel.KeyQuotaStatus(i) != common.ChannelStatusQuotaPaused {
			continue
		}
		changed, err := ResumeChannelKey(channel, key)
		if err != nil {
			return resumed, err
		}
		if changed {
			resumed++
		}
	}
	return resumed, nil
}

// ResumeChannelKeysBelowQuota enables every quota-paused key whose current
// period usage is below its stop line, or that no quota applies to any more.
// It reads only stored state, so it is safe to run after a restart.
func ResumeChannelKeysBelowQuota(now time.Time) error {
	var channels []*model.Channel
	if err := model.DB.Find(&channels).Error; err != nil {
		return err
	}
	for _, channel := range channels {
		for i, key := range channel.QuotaKeys() {
			if channel.KeyQuotaStatus(i) != common.ChannelStatusQuotaPaused {
				continue
			}
			fingerprint := model.KeyFingerprint(key)
			_, cfg := channel.EffectiveKeyQuota(fingerprint)
			var periodStart int64
			if cfg != nil {
				periodStart = model.QuotaPeriodStart(*cfg, now).Unix()
				usage, err := model.GetChannelKeyUsage(channel.Id, fingerprint, periodStart)
				if err != nil {
					common.SysError(common.LogText("failed to read channel key usage: channel_id=%d, key=%s, error=%v", channel.Id, fingerprint, err))
					continue
				}
				if model.KeyQuotaStopReached(*cfg, usage.UsedQuota) {
					continue
				}
			}
			changed, err := model.SetChannelKeyQuotaPaused(channel.Id, fingerprint, false, "")
			if err != nil {
				common.SysError(common.LogText("failed to resume channel key: channel_id=%d, key=%s, error=%v", channel.Id, fingerprint, err))
				continue
			}
			if !changed {
				continue
			}
			common.SysLog(common.LogText("channel key resumed after its quota reset: channel_id=%d, key=%s", channel.Id, fingerprint))
			notify := true
			if cfg != nil {
				marked, err := model.MarkChannelKeyUsage(channel.Id, fingerprint, periodStart, "resumed_at")
				notify = err == nil && marked
			}
			if notify {
				notifyKeyQuota(channel, key, "resumed", i18n.MsgChannelKeyQuotaResumedSubject, i18n.MsgChannelKeyQuotaResumedContent, map[string]any{})
			}
		}
	}
	return nil
}

// StartChannelKeyQuotaResumer resumes quota-paused keys every minute on the
// master node.
func StartChannelKeyQuotaResumer() {
	channelKeyQuotaResumeOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			ticker := time.NewTicker(channelKeyQuotaResumeInterval)
			defer ticker.Stop()
			for {
				runChannelKeyQuotaResumeOnce()
				<-ticker.C
			}
		})
	})
}

func runChannelKeyQuotaResumeOnce() {
	if !channelKeyQuotaResumeRunning.CompareAndSwap(false, true) {
		return
	}
	defer channelKeyQuotaResumeRunning.Store(false)
	if err := ResumeChannelKeysBelowQuota(time.Now()); err != nil {
		logger.LogWarn(context.Background(), common.LogText("channel key quota resume task failed: %v", err))
	}
}
