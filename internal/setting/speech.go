// internal/setting/speech.go

package setting

// 语音合成配置在配置表中的键名。点号分层，镜像 YAML 的层级路径。
const (
	KeySpeechVoice        = "speech.voice"
	KeySpeechInstructions = "speech.instructions"
)

// SpeechSettings 语音合成（文本转语音）配置。
// 两个字段只影响 Web 界面的朗读，不影响对外语音合成接口的参数取用；
// 均为空串时不向 TTS 模型下发对应字段，由模型取其默认行为。
type SpeechSettings struct {
	// Voice 音色，各模型可选值不同
	Voice string
	// Instructions 风格指令，如「用轻快的语气朗读」，仅支持指令的模型有效
	Instructions string
}
