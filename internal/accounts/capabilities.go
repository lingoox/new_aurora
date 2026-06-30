package accounts

// Capability 表示一个功能对账号类型的要求
// ★ 当 ChatGPT 策略变化时，只改这个文件的对应常量 ★
type Capability struct {
	Name          string
	RequiresPUID  bool // 需要付费账号
	RequiresLogin bool // 需要登录（free 或 puid）
}

// 系统内所有功能及其当前账号要求
var (
	CapChat           = Capability{Name: "chat"}
	CapResponses      = Capability{Name: "responses"}
	CapToolCalling    = Capability{Name: "tool_calling"}
	CapImageGenerate  = Capability{Name: "image_generation", RequiresPUID: true}
	CapImageEdit      = Capability{Name: "image_edit", RequiresPUID: true}
	CapImageVariation = Capability{Name: "image_variation", RequiresPUID: true}
	CapTTS            = Capability{Name: "tts", RequiresPUID: true}
	CapTranscribe     = Capability{Name: "transcribe", RequiresPUID: true}
	CapFileUpload     = Capability{Name: "file_upload", RequiresPUID: true}
)

// Satisfies 判断账号类型是否满足某项能力要求
func (t AccountType) Satisfies(cap Capability) bool {
	switch t {
	case TypePUID:
		return true
	case TypeFree:
		return !cap.RequiresPUID
	case TypeNoAuth:
		return !cap.RequiresPUID && !cap.RequiresLogin
	default:
		return false
	}
}
