// internal/setting/migrate_legacy.go
// 老 config.yaml 业务项到配置表的一次性写入。
package setting

import (
	"context"
	"strconv"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// ImportLegacy 把老 config.yaml 解析出的业务项写入配置表。
//
// 两条规则：
//   - 只写非零值：老模板几乎全注释，零值代表「使用者没配过」，写进表里
//     会把无效值（如 max_iterations=0）固化成显式设置。
//   - 表内已有的键跳过：表值来自使用者后来在界面上的改动，比老 YAML 新。
//
// 布尔项特殊：false 与零值不可区分，只在 true 时写入。默认值本就是
// false，因此丢失「显式的 false」不改变行为。
//
// RateLimit.CleanupInterval 不写表：它是 bootstrap.yaml 的项。
func (s *Settings) ImportLegacy(ctx context.Context, lb *config.LegacyBusiness) error {
	if lb == nil {
		return nil
	}
	if s.repo == nil {
		return ErrNoSettingStore
	}
	existing, err := s.globalValues(ctx)
	if err != nil {
		return err
	}

	var rows []*repo.Setting
	add := func(key, value string) {
		if _, ok := existing[key]; ok {
			return
		}
		rows = append(rows, &repo.Setting{Scope: repo.ScopeGlobal, Name: key, Value: value})
	}
	addInt := func(key string, v int) {
		if v != 0 {
			add(key, strconv.Itoa(v))
		}
	}
	addFloat := func(key string, v float64) {
		if v != 0 {
			add(key, formatFloat(v))
		}
	}
	addStr := func(key, v string) {
		if v != "" {
			add(key, v)
		}
	}
	addTrue := func(key string, v bool) {
		if v {
			add(key, "true")
		}
	}

	addInt(KeyMemoryHistoryWindow, lb.Memory.HistoryWindow)

	addInt(KeyReactMaxIterations, lb.React.MaxIterations)
	addInt(KeyReactStepTimeout, lb.React.StepTimeout)
	addInt(KeyReactErrorRetry, lb.React.ErrorRetry)

	addInt(KeySubAgentMaxConcurrency, lb.SubAgent.MaxConcurrency)
	addStr(KeySubAgentExecTimeout, lb.SubAgent.ExecTimeout)
	addInt(KeySubAgentMaxTaskLength, lb.SubAgent.MaxTaskLength)
	addInt(KeySubAgentMaxResultLength, lb.SubAgent.MaxResultLength)

	addInt(KeyAttachmentMaxSize, lb.Attachment.MaxSize)
	addInt(KeyAttachmentMaxTotalSize, lb.Attachment.MaxTotalSize)
	addInt(KeyAttachmentMaxCount, lb.Attachment.MaxCount)
	if len(lb.Attachment.AllowedTypes) > 0 {
		add(KeyAttachmentAllowedTypes, encodeAllowedTypes(lb.Attachment.AllowedTypes))
	}

	addTrue(KeyRateLimitEnabled, lb.RateLimit.Enabled)
	addFloat(KeyRateLimitGlobalQPS, lb.RateLimit.GlobalQPS)
	addInt(KeyRateLimitGlobalConcurrency, lb.RateLimit.GlobalConcurrency)
	addFloat(KeyRateLimitDefaultQPS, lb.RateLimit.DefaultQPS)
	addInt(KeyRateLimitDefaultConcurrency, lb.RateLimit.DefaultConcurrency)

	addTrue(KeyScheduleEnabled, lb.ScheduleEnabled)

	addStr(KeyAuthSecret, lb.Auth.Secret)
	addStr(KeyAuthHeaderName, lb.Auth.HeaderName)

	for name, conf := range lb.Senders {
		if name != SenderWebhook {
			continue // 注册表里没有实现的渠道不迁移
		}
		addTrue(senderKey(name, fieldEnabled), conf.Enabled)
		addStr(senderKey(name, fieldURL), conf.URL)
	}

	if len(rows) == 0 {
		return nil
	}
	return s.repo.Upsert(ctx, rows...)
}
