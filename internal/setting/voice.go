// internal/setting/voice.go

package setting

// 语音配置在配置表中的键名。点号分层，镜像 YAML 的层级路径。
const (
	KeyVoiceModel    = "voice.model"
	KeyVoiceAutoSend = "voice.auto_send"
)

// VoiceSettings 语音输入配置。
type VoiceSettings struct {
	// Model Web 界面语音输入使用的识别模型，空串表示不启用语音输入。
	// 只影响界面，不影响对外转录接口的模型取用
	Model string
	// AutoSend 转录完成后是否自动发送
	AutoSend bool
}
