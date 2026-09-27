// internal/setting/voice.go

package setting

// 语音配置在配置表中的键名。点号分层，镜像 YAML 的层级路径。
const (
	KeyVoiceEnabled  = "voice.enabled"
	KeyVoiceModel    = "voice.model"
	KeyVoiceAutoSend = "voice.auto_send"
)

// VoiceSettings 语音输入配置。
type VoiceSettings struct {
	// Enabled 聊天页是否显示话筒按钮。只影响界面，不影响对外转录接口的可用性
	Enabled bool
	// Model 用于转录的模型名，空串表示尚未配置
	Model string
	// AutoSend 转录完成后是否自动发送
	AutoSend bool
}
