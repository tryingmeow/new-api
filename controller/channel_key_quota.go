package controller

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const maxChannelKeyNameLength = 64

type channelKeyRequest struct {
	Fingerprint string                 `json:"fingerprint"`
	Name        string                 `json:"name"`
	QuotaMode   string                 `json:"quota_mode"`
	Quota       *kitdto.KeyQuotaConfig `json:"quota"`
}

// attachKeyQuotaSummaries sets the current-period key quota summary of every
// listed channel. The listed channels carry no key, so keys are read here and
// never left on the response.
func attachKeyQuotaSummaries(channels []*model.Channel) {
	ids := make([]int, 0, len(channels))
	for _, channel := range channels {
		channel.KeyQuotaSummary = &model.KeyQuotaSummary{}
		cfg := channel.ChannelKeyQuota()
		if cfg != nil && cfg.Enabled || len(channel.ChannelInfo.KeyMeta) > 0 {
			ids = append(ids, channel.Id)
			continue
		}
		if !channel.ChannelInfo.IsMultiKey {
			if channel.Status == common.ChannelStatusQuotaPaused {
				channel.KeyQuotaSummary.PausedKeys = 1
			}
			continue
		}
		for _, status := range channel.ChannelInfo.MultiKeyStatusList {
			if status == common.ChannelStatusQuotaPaused {
				channel.KeyQuotaSummary.PausedKeys++
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	keys, err := model.GetChannelKeysByIDs(ids)
	if err != nil {
		common.SysError(common.LogText("failed to load channel keys for quota summary: %v", err))
		return
	}
	usages, err := model.GetChannelKeyUsages(ids)
	if err != nil {
		common.SysError(common.LogText("failed to load channel key usages: %v", err))
		return
	}
	now := time.Now()
	for _, channel := range channels {
		key, ok := keys[channel.Id]
		if !ok {
			continue
		}
		withKey := *channel
		withKey.Key, withKey.Keys = key, nil
		channel.KeyQuotaSummary = service.BuildKeyQuotaSummary(service.BuildChannelKeyRows(&withKey, usages[channel.Id], now))
	}
}

func loadChannelWithKeys(c *gin.Context) (*model.Channel, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	channel, err := model.GetChannelById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	return channel, true
}

func channelKeysPayload(channel *model.Channel) (gin.H, error) {
	usages, err := model.GetChannelKeyUsages([]int{channel.Id})
	if err != nil {
		return nil, err
	}
	return gin.H{
		"is_multi_key": channel.ChannelInfo.IsMultiKey,
		"quota":        channel.ChannelKeyQuota(),
		"keys":         service.BuildChannelKeyRows(channel, usages[channel.Id], time.Now()),
	}, nil
}

func respondChannelKeys(c *gin.Context, channelID int) {
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	payload, err := channelKeysPayload(channel)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, payload)
}

// bindChannelKeyRequest reads the request body and finds the key it names.
func bindChannelKeyRequest(c *gin.Context, channel *model.Channel) (channelKeyRequest, string, bool) {
	var request channelKeyRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, err)
		return request, "", false
	}
	for _, key := range channel.QuotaKeys() {
		if model.KeyFingerprint(key) == request.Fingerprint {
			return request, key, true
		}
	}
	common.ApiErrorT(c, "Channel key not found")
	return request, "", false
}

// GetChannelKeys lists the keys of a channel with their names and quota state.
func GetChannelKeys(c *gin.Context) {
	channel, ok := loadChannelWithKeys(c)
	if !ok {
		return
	}
	payload, err := channelKeysPayload(channel)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, payload)
}

// UpdateChannelKeyMeta saves the name and quota mode of one key.
func UpdateChannelKeyMeta(c *gin.Context) {
	channel, ok := loadChannelWithKeys(c)
	if !ok {
		return
	}
	request, _, ok := bindChannelKeyRequest(c, channel)
	if !ok {
		return
	}
	meta := model.KeyMeta{Name: strings.TrimSpace(request.Name), QuotaMode: request.QuotaMode}
	if len([]rune(meta.Name)) > maxChannelKeyNameLength {
		common.ApiErrorT(c, "Key name is too long")
		return
	}
	switch meta.QuotaMode {
	case "", model.KeyQuotaModeInherit:
		meta.QuotaMode = model.KeyQuotaModeInherit
	case model.KeyQuotaModeCustom:
		if request.Quota == nil {
			common.ApiErrorT(c, "A custom key quota is required")
			return
		}
		if err := request.Quota.Validate(); err != nil {
			common.ApiError(c, err)
			return
		}
		meta.Quota = request.Quota
	default:
		common.ApiErrorT(c, "Invalid key quota mode")
		return
	}
	if err := model.SaveChannelKeyMeta(channel.Id, request.Fingerprint, meta); err != nil {
		if errors.Is(err, model.ErrChannelKeyNotFound) {
			common.ApiErrorT(c, "Channel key not found")
			return
		}
		common.ApiError(c, err)
		return
	}
	respondChannelKeys(c, channel.Id)
}

// ResetChannelKeyUsage zeroes the current-period usage of one key and
// enables it again when it is quota paused.
func ResetChannelKeyUsage(c *gin.Context) {
	channel, ok := loadChannelWithKeys(c)
	if !ok {
		return
	}
	request, key, ok := bindChannelKeyRequest(c, channel)
	if !ok {
		return
	}
	if _, cfg := channel.EffectiveKeyQuota(request.Fingerprint); cfg != nil {
		periodStart := model.QuotaPeriodStart(*cfg, time.Now()).Unix()
		if err := model.ResetChannelKeyUsage(channel.Id, request.Fingerprint, periodStart); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	if _, err := service.ResumeChannelKey(channel, key); err != nil {
		common.ApiError(c, err)
		return
	}
	respondChannelKeys(c, channel.Id)
}

// ResumeChannelKey enables a quota-paused key now, keeping its usage. A key
// still over its stop line is paused again by its next request.
func ResumeChannelKey(c *gin.Context) {
	channel, ok := loadChannelWithKeys(c)
	if !ok {
		return
	}
	_, key, ok := bindChannelKeyRequest(c, channel)
	if !ok {
		return
	}
	if _, err := service.ResumeChannelKey(channel, key); err != nil {
		common.ApiError(c, err)
		return
	}
	respondChannelKeys(c, channel.Id)
}

// ResumeChannelPausedKeys enables every quota-paused key of a channel now,
// keeping usage. Keys still over their stop line pause again on next use.
func ResumeChannelPausedKeys(c *gin.Context) {
	channel, ok := loadChannelWithKeys(c)
	if !ok {
		return
	}
	if _, err := service.ResumeChannelPausedKeys(channel); err != nil {
		common.ApiError(c, err)
		return
	}
	respondChannelKeys(c, channel.Id)
}
