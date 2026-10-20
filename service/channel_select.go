package service

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

func GetChannelConstraints(c *gin.Context) *dto.ChannelConstraints {
	if c == nil {
		return &dto.ChannelConstraints{}
	}
	if existing, ok := common.GetContextKeyType[*dto.ChannelConstraints](c, constant.ContextKeyChannelConstraints); ok && existing != nil {
		return existing
	}
	constraints := &dto.ChannelConstraints{}
	common.SetContextKey(c, constant.ContextKeyChannelConstraints, constraints)
	return constraints
}

func AppendTaskPluginIdentityFilter(c *gin.Context, pluginKey string) {
	if c == nil {
		return
	}
	channelTypes, pluginKeys := pinnedTaskPluginIdentities(c, pluginKey)
	GetChannelConstraints(c).AddFilter(dto.ChannelFilter{
		Kind:                   dto.FilterTaskPluginIdentity,
		TaskPluginKey:          pluginKey,
		TaskPluginChannelTypes: channelTypes,
		TaskPluginKeys:         pluginKeys,
	})
}

type RetryParam struct {
	Ctx          *gin.Context
	TokenGroup   string
	ModelName    string
	RequestPath  string
	Retry        *int
	resetNextTry bool
}

func (p *RetryParam) GetRetry() int {
	if p.Retry == nil {
		return 0
	}
	return *p.Retry
}

func (p *RetryParam) SetRetry(retry int) {
	p.Retry = &retry
}

func (p *RetryParam) IncreaseRetry() {
	if p.resetNextTry {
		p.resetNextTry = false
		return
	}
	if p.Retry == nil {
		p.Retry = new(int)
	}
	*p.Retry++
}

func (p *RetryParam) ResetRetryNextTry() {
	p.resetNextTry = true
}

// CancelResetRetryNextTry undoes a pending ResetRetryNextTry. Selection marks the
// reset while walking towards another group; if the walk finds no group able to
// take over, the walk is abandoned and the counter must resume as normal or the
// retry loop would never reach its bound.
// 选路在走向下一个分组时会预置重置；若最终没有分组能接手而放弃换组，必须撤销该预置，
// 否则重试计数器不再前进，外层循环永远到不了上界。
func (p *RetryParam) CancelResetRetryNextTry() {
	p.resetNextTry = false
}

// CacheGetRandomSatisfiedChannel tries to get a random channel that satisfies the requirements.
// 尝试获取一个满足要求的随机渠道。
//
// For "auto" tokenGroup with cross-group Retry enabled:
// 对于启用了跨分组重试的 "auto" tokenGroup：
//
//   - Each group will exhaust all its priorities before moving to the next group.
//     每个分组会用完所有优先级后才会切换到下一个分组。
//
//   - Uses ContextKeyAutoGroupIndex to track current group index.
//     使用 ContextKeyAutoGroupIndex 跟踪当前分组索引。
//
//   - Uses ContextKeyAutoGroupRetryIndex to track priorityRetry, the priority level
//     within the current group. It lives on the request context rather than on
//     RetryParam because the distributor and the relay retry loop each build their
//     own RetryParam, so anything kept only on the param is lost between them.
//     使用 ContextKeyAutoGroupRetryIndex 跟踪 priorityRetry，即当前分组内的优先级级别。
//     它存放在请求 context 而非 RetryParam 上：distributor 与 relay 重试循环各自新建
//     RetryParam，只存在 param 上的状态会在两者之间丢失。
//
//   - When GetRandomSatisfiedChannel returns nil (no channel for the model), moves to next group.
//     当 GetRandomSatisfiedChannel 返回 nil（该模型无可用渠道）时，切换到下一个分组。
//
//   - A group is never dialled more times than it has channels for the model, nor
//     more than RetryTimes+1 times. The first bound is checked before selecting, so
//     a group that is already used up costs no attempt; the second after, so the
//     current attempt still uses the group it was chosen from. Counting channels
//     rather than priority levels is what lets several channels on one level still
//     take turns; whether a given level is revisited depends on how the budget and
//     the ladder line up, so this is a bound, not a guarantee of sibling coverage.
//     一个分组被拨打的次数不会超过它为该模型准备的渠道数，也不会超过 RetryTimes+1 次。
//     前者在选路前判断，因此已用尽的分组不消耗尝试；后者在选路后判断，因此本次尝试仍用
//     当前分组。按渠道数而非优先级层数计数，才使同一层上的多个渠道有机会轮到；某一层是否
//     会被再次进入取决于预算与阶梯的相对长短，因此这是一个上限，而非同层必被覆盖的保证。
//
// Example flow (GroupA has 1 priority, GroupB has 2, RetryTimes=2):
// 示例流程（分组A 有1个优先级，分组B 有2个，RetryTimes=2）：
//
//	attempt 0 (distributor): GroupA, priority0 (priorityRetry=0)
//	                         分组A, 优先级0
//
//	attempt 1: priorityRetry=1 walks past GroupA's ladder → GroupB, priority0
//	           priorityRetry=1 越过分组A的阶梯 → 分组B, 优先级0
//
//	attempt 2: GroupB, priority1
//	           分组B, 优先级1
func CacheGetRandomSatisfiedChannel(param *RetryParam) (*model.Channel, string, error) {
	var channel *model.Channel
	var err error
	selectGroup := param.TokenGroup
	userGroup := common.GetContextKeyString(param.Ctx, constant.ContextKeyUserGroup)
	filters := GetChannelConstraints(param.Ctx).Filters

	if param.TokenGroup == "auto" {
		autoGroups := GetRequestAutoGroups(param.Ctx, userGroup)
		if len(autoGroups) == 0 {
			return nil, selectGroup, errors.New("auto groups is not enabled")
		}

		// startGroupIndex: the group index to start searching from
		// startGroupIndex: 开始搜索的分组索引
		startGroupIndex := 0
		crossGroupRetry := common.GetContextKeyBool(param.Ctx, constant.ContextKeyTokenCrossGroupRetry)

		if lastGroupIndex, exists := common.GetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex); exists {
			if idx, ok := lastGroupIndex.(int); ok {
				startGroupIndex = idx
			}
		}
		// priorityRetry is the priority level within the current group, carried on
		// the request context so it survives the RetryParam swap between the
		// distributor and the relay retry loop.
		// priorityRetry 是当前分组内的优先级级别，存放在请求 context 上，因此能跨越
		// distributor 与 relay 重试循环之间的 RetryParam 更替。
		priorityRetry := 0
		if lastPriorityRetry, exists := common.GetContextKey(param.Ctx, constant.ContextKeyAutoGroupRetryIndex); exists {
			if idx, ok := lastPriorityRetry.(int); ok {
				priorityRetry = idx
			}
		}
		// Capping a group's attempts only pays off when another group can take the
		// model over. Remember the first group given up on, so that a walk which
		// reaches the end without finding a successor can return to it instead of
		// leaving the request with no channel at all — otherwise a model served by a
		// single channel would lose retries entirely rather than fail over.
		// 限制一个分组的尝试次数，只有在别的分组能接手该模型时才划算。记住第一个被放弃的
		// 分组：若走到尽头都没有分组能接手，就退回它继续重试，而不是让请求无渠道可用。
		// 否则只有单个渠道承载的模型会彻底失去重试，而不是转移到别处。
		retryBeforeWalk := param.GetRetry()
		var givenUpChannel *model.Channel
		givenUpGroup := ""
		givenUpIndex := 0
		givenUpPriorityRetry := 0

		for i := startGroupIndex; i < len(autoGroups); i++ {
			autoGroup := autoGroups[i]
			// If moved to a new group, restart from its highest priority
			// 如果切换到新分组，从它的最高优先级重新开始
			if i > startGroupIndex {
				priorityRetry = 0
			}
			logger.LogDebug(param.Ctx, "Auto selecting group: %s, priorityRetry: %d", autoGroup, priorityRetry)

			var channelCount int
			channel, channelCount, _ = model.GetRandomSatisfiedChannel(
				autoGroup,
				param.ModelName,
				priorityRetry,
				filters,
			)
			if channel == nil {
				// Current group has no available channel for this model, try next group
				// 当前分组没有该模型的可用渠道，尝试下一个分组
				logger.LogDebug(param.Ctx, "No available channel in group %s for model %s at priorityRetry %d, trying next group", autoGroup, param.ModelName, priorityRetry)
				// 重置状态以尝试下一个分组
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupRetryIndex, 0)
				// Reset retry counter so outer loop can continue for next group
				// 重置重试计数器，以便外层循环可以为下一个分组继续
				param.SetRetry(0)
				continue
			}
			if crossGroupRetry && priorityRetry > channelCount-1 {
				if givenUpChannel == nil {
					givenUpChannel = channel
					givenUpGroup = autoGroup
					givenUpIndex = i
					givenUpPriorityRetry = priorityRetry
				}
				// This group has already been dialled as many times as it has channels
				// for the model, so another attempt can only repeat one that already
				// failed — retry past the ladder just clamps back to the lowest
				// priority. Move on instead of spending an attempt here.
				// 该分组已被拨打过与其渠道数一样多的次数，再试只会重复拨打已经失败的渠道
				// （retry 超出优先级数量后会被 clamp 回最低优先级）。直接换组，不浪费这次尝试。
				logger.LogDebug(param.Ctx, "Group %s exhausted for model %s (priorityRetry=%d, channelCount=%d), trying next group", autoGroup, param.ModelName, priorityRetry, channelCount)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupRetryIndex, 0)
				param.SetRetry(0)
				param.ResetRetryNextTry()
				channel = nil
				continue
			}
			common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroup, autoGroup)
			selectGroup = autoGroup
			logger.LogDebug(param.Ctx, "Auto selected group: %s", autoGroup)

			// Prepare state for next retry
			// 为下一次重试准备状态
			if crossGroupRetry && priorityRetry >= common.RetryTimes {
				// Current group has exhausted its retry budget, prepare to switch to next group
				// This request still uses current group, but next retry will use next group
				// 当前分组已用完重试预算，准备切换到下一个分组
				// 本次请求仍使用当前分组，但下次重试将使用下一个分组
				logger.LogDebug(param.Ctx, "Current group %s retry budget exhausted (priorityRetry=%d >= RetryTimes=%d), preparing switch to next group for next retry", autoGroup, priorityRetry, common.RetryTimes)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupRetryIndex, 0)
				// Reset retry counter so outer loop can continue for next group
				// 重置重试计数器，以便外层循环可以为下一个分组继续
				param.SetRetry(0)
				param.ResetRetryNextTry()
			} else {
				// Stay in current group, descend one priority level on the next retry
				// 保持在当前分组，下次重试降一级优先级
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupRetryIndex, priorityRetry+1)
			}
			break
		}
		if channel == nil && givenUpChannel != nil {
			// Only resume when no other group carries the model at all. If one does,
			// this request has already failed over to it and the cap has done its
			// job; resuming would just re-dial a channel whose turn is over.
			// 仅当确实没有任何其他分组承载该模型时才回退。若有，说明本次请求已经完成过
			// 故障转移、上限已发挥作用，再回退只会重复拨打一个已经轮过的渠道。
			servedElsewhere := false
			for j, otherGroup := range autoGroups {
				if j == givenUpIndex {
					continue
				}
				if other, _, _ := model.GetRandomSatisfiedChannel(otherGroup, param.ModelName, 0, filters); other != nil {
					servedElsewhere = true
					break
				}
			}
			if servedElsewhere {
				return nil, selectGroup, nil
			}
			// No group can take over this model, so the group given up on above is
			// still the best available; resume retrying it under the RetryTimes bound.
			// 没有任何分组能接手该模型，上面放弃的那个分组仍是最优选择，
			// 回到它并在 RetryTimes 约束下继续重试。
			logger.LogDebug(param.Ctx, "No group can take over model %s, resuming group %s", param.ModelName, givenUpGroup)
			channel = givenUpChannel
			selectGroup = givenUpGroup
			common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroup, givenUpGroup)
			common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, givenUpIndex)
			common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupRetryIndex, givenUpPriorityRetry+1)
			param.SetRetry(retryBeforeWalk)
			param.CancelResetRetryNextTry()
		}
	} else {
		channel, _, err = model.GetRandomSatisfiedChannel(
			param.TokenGroup,
			param.ModelName,
			param.GetRetry(),
			filters,
		)
		if err != nil {
			return nil, param.TokenGroup, err
		}
	}
	return channel, selectGroup, nil
}

func pinnedTaskPluginIdentities(c *gin.Context, expected string) ([]int, []string) {
	if c == nil || expected == "" {
		return nil, nil
	}
	if value, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint); exists {
		pinned, ok := value.(jsplugin.PinnedEndpoint)
		if ok && pinned.Generation != nil && len(pinned.Candidates) > 1 {
			expectedFound := false
			channelTypes := make([]int, 0, len(pinned.Candidates))
			pluginKeys := make([]string, 0, len(pinned.Candidates))
			seen := make(map[int]struct{}, len(pinned.Candidates))
			for _, candidate := range pinned.Candidates {
				if candidate.Plugin == nil {
					continue
				}
				if candidate.Plugin.Meta.Key == expected {
					expectedFound = true
				}
				pluginKeys = append(pluginKeys, candidate.Plugin.Meta.Key)
				for _, channelType := range candidate.Plugin.Meta.ChannelTypes {
					if channelType == 0 || channelType == constant.ChannelTypeTaskPlugin {
						continue
					}
					if _, duplicate := seen[channelType]; duplicate {
						continue
					}
					if plugin, indexed := pinned.Generation.GetByChannelType(channelType); indexed && plugin == candidate.Plugin {
						seen[channelType] = struct{}{}
						channelTypes = append(channelTypes, channelType)
					}
				}
			}
			if expectedFound {
				return channelTypes, pluginKeys
			}
		}
	}
	value, exists := c.Get(jsplugin.ContextKeyPinnedPlugin)
	pinned, ok := value.(jsplugin.PinnedPlugin)
	if !exists || !ok || pinned.Generation == nil || pinned.Plugin == nil || pinned.Plugin.Meta.Key != expected {
		return nil, nil
	}
	channelTypes := make([]int, 0, len(pinned.Plugin.Meta.ChannelTypes))
	for _, channelType := range pinned.Plugin.Meta.ChannelTypes {
		if channelType == 0 || channelType == constant.ChannelTypeTaskPlugin {
			continue
		}
		channelTypes = append(channelTypes, channelType)
	}
	return channelTypes, []string{expected}
}

// ChannelSelectError explains why SelectChannelForRequest found no channel.
// Callers render it for their transport: the HTTP distributor localizes
// MessageID with its own helpers and the Responses WebSocket relay wraps it in
// a NewAPIError. Message is set instead of MessageID when the text is a fixed
// error code that clients match on.
type ChannelSelectError struct {
	StatusCode int
	Code       types.ErrorCode
	MessageID  string
	Params     map[string]any
	Message    string
	// FilterKind and Channel identify a candidate rejected by request filters.
	FilterKind dto.ChannelFilterKind
	Channel    *model.Channel
	// NoAvailableChannel marks the "no channel for this group and model"
	// outcome so the distributor can name the claiming task plugin.
	NoAvailableChannel bool
}

// SelectChannelForRequest resolves the channel for one attempt with the rules
// shared by the HTTP distributor and the Responses WebSocket relay: a pinned
// channel wins, then session affinity (first attempt only), then a random
// eligible channel; every candidate must satisfy the request's channel
// filters. The group the channel was chosen from is returned for auto-group
// callers. The caller still applies SetupContextForSelectedChannel.
func SelectChannelForRequest(c *gin.Context, modelName string, retry *RetryParam) (*model.Channel, string, *ChannelSelectError) {
	constraints := GetChannelConstraints(c)
	if pin, found, overridden := constraints.ResolvedPin(); found {
		for _, lost := range overridden {
			logger.LogWarn(c, common.LogText(
				"channel pin overridden: winning_source=%s winning_channel_id=%d overridden_source=%s overridden_channel_id=%d",
				pin.Source, pin.ChannelId, lost.Source, lost.ChannelId,
			))
		}
		channel, err := model.CacheGetChannel(pin.ChannelId)
		if err != nil {
			return nil, "", pinnedChannelUnavailable(pin, http.StatusBadRequest, i18n.MsgDistributorInvalidChannelId)
		}
		if channel.Status != common.ChannelStatusEnabled {
			return nil, "", pinnedChannelUnavailable(pin, http.StatusForbidden, i18n.MsgDistributorChannelDisabled)
		}
		if ok, kind := model.ChannelSatisfiesFilters(channel, modelName, constraints.Filters); !ok {
			return nil, "", &ChannelSelectError{
				StatusCode: http.StatusBadRequest, Code: types.ErrorCode(kind), MessageID: i18n.MsgDistributorNoAvailableChannel,
				Params:     map[string]any{"Group": common.GetContextKeyString(c, constant.ContextKeyUsingGroup), "Model": modelName},
				FilterKind: kind, Channel: channel,
			}
		}
		return channel, "", nil
	}

	usingGroup := retry.TokenGroup
	var channel *model.Channel
	var selectGroup string
	if retry.GetRetry() == 0 {
		if preferredChannelID, found := GetPreferredChannelByAffinity(c, modelName, usingGroup); found {
			affinityUsable := false
			preferred, err := model.CacheGetChannel(preferredChannelID)
			affinitySatisfied := false
			if err == nil && preferred != nil && preferred.Status == common.ChannelStatusEnabled {
				affinitySatisfied, _ = model.ChannelSatisfiesFilters(preferred, modelName, constraints.Filters)
			}
			if affinitySatisfied {
				if usingGroup == "auto" {
					userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
					for _, g := range GetRequestAutoGroups(c, userGroup) {
						if model.IsChannelEnabledForGroupModel(g, modelName, preferred.Id) {
							selectGroup = g
							common.SetContextKey(c, constant.ContextKeyAutoGroup, g)
							channel = preferred
							affinityUsable = true
							MarkChannelAffinityUsed(c, g, preferred.Id)
							break
						}
					}
				} else if model.IsChannelEnabledForGroupModel(usingGroup, modelName, preferred.Id) {
					channel = preferred
					selectGroup = usingGroup
					affinityUsable = true
					MarkChannelAffinityUsed(c, usingGroup, preferred.Id)
				}
			}
			if !affinityUsable && !ShouldKeepChannelAffinityOnChannelDisabled() {
				ClearCurrentChannelAffinityCache(c)
			}
			if !affinityUsable && RequestPolicy(c).SessionMode == "strict" {
				return nil, "", &ChannelSelectError{StatusCode: http.StatusServiceUnavailable, Message: "strict_session_binding_unavailable"}
			}
		}
	}

	if channel == nil {
		var err error
		channel, selectGroup, err = CacheGetRandomSatisfiedChannel(retry)
		if err != nil {
			showGroup := usingGroup
			if usingGroup == "auto" {
				showGroup = fmt.Sprintf("auto(%s)", selectGroup)
			}
			return nil, selectGroup, &ChannelSelectError{
				StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeModelNotFound, MessageID: i18n.MsgDistributorGetChannelFailed,
				Params: map[string]any{"Group": showGroup, "Model": modelName, "Error": err.Error()},
			}
		}
		if channel == nil {
			return nil, selectGroup, &ChannelSelectError{
				StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeModelNotFound, MessageID: i18n.MsgDistributorNoAvailableChannel,
				Params: map[string]any{"Group": usingGroup, "Model": modelName}, NoAvailableChannel: true,
			}
		}
	}
	if ok, kind := model.ChannelSatisfiesFilters(channel, modelName, constraints.Filters); !ok {
		return nil, selectGroup, &ChannelSelectError{
			StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeModelNotFound, MessageID: i18n.MsgDistributorNoAvailableChannel,
			Params:     map[string]any{"Group": common.GetContextKeyString(c, constant.ContextKeyUsingGroup), "Model": modelName},
			FilterKind: kind, Channel: channel, NoAvailableChannel: true,
		}
	}
	return channel, selectGroup, nil
}

// Origin-task pins report a fixed code so task polling can tell a retired
// channel from a malformed request.
func pinnedChannelUnavailable(pin dto.ChannelPin, statusCode int, messageID string) *ChannelSelectError {
	if pin.Source == dto.PinSourceOriginTask {
		return &ChannelSelectError{StatusCode: http.StatusBadRequest, Code: "origin_task_channel_disabled", Message: "origin_task_channel_disabled"}
	}
	return &ChannelSelectError{StatusCode: statusCode, MessageID: messageID}
}

// AppendUsedChannel records an attempted channel in the request's channel
// trail, which the retry log and the consume log's admin_info both read.
func AppendUsedChannel(c *gin.Context, channelID int) {
	c.Set("use_channel", append(c.GetStringSlice("use_channel"), fmt.Sprintf("%d", channelID)))
}
