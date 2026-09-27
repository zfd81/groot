// internal/setting/defaults.go
// 配置默认值集中定义在此，一个分类一组。

package setting

import "github.com/zfd81/groot/internal/config"

// defaultVoice 返回语音配置的默认值。
// 默认关闭：话筒按钮需要使用者先指定转录模型才有意义。
func defaultVoice() VoiceSettings {
	return VoiceSettings{
		Enabled:  false,
		Model:    "",
		AutoSend: false,
	}
}

// 以下各函数是配置表分类的基准层。表中缺失的键回落到这里，
// 因此配置表在创建后为空即可工作。

// defaultMemory 记忆模块默认值。
func defaultMemory() config.MemoryConfig {
	return config.MemoryConfig{HistoryWindow: 20}
}

// defaultReact ReAct 循环默认值。
func defaultReact() config.ReactConfig {
	return config.ReactConfig{MaxIterations: 20, StepTimeout: 60, ErrorRetry: 2}
}

// defaultSubAgent 子 Agent 调度默认值。
func defaultSubAgent() config.SubAgentConfig {
	return config.SubAgentConfig{
		MaxConcurrency:  5,
		ExecTimeout:     "5m",
		MaxTaskLength:   16000,
		MaxResultLength: 8000,
	}
}

// defaultAttachment 附件默认值。AllowedTypes 为空表示允许所有类型。
func defaultAttachment() config.AttachmentConfig {
	return config.AttachmentConfig{MaxSize: 50, MaxTotalSize: 100, MaxCount: 10}
}

// defaultRateLimit 限流默认值。CleanupInterval 不在此列：
// 它是后台回收协程的周期，恒取 bootstrap.yaml 的值。
func defaultRateLimit() config.RateLimitConfig {
	return config.RateLimitConfig{
		Enabled:            false,
		GlobalQPS:          0,
		GlobalConcurrency:  0,
		DefaultQPS:         10,
		DefaultConcurrency: 5,
	}
}

// defaultScheduleEnabled 定时任务默认关闭：
// 允许模型自行创建定时任务是一项需要明确开启的策略。
func defaultScheduleEnabled() bool { return false }

// DefaultAuthHeaderName API Key 请求头的默认名称。
// 导出供接口层在响应中标注默认值（界面「恢复默认」提示用）。
const DefaultAuthHeaderName = "X-API-Key"

// defaultAuthHeaderName API Key 请求头的默认名称。
func defaultAuthHeaderName() string { return DefaultAuthHeaderName }
