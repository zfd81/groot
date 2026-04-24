package apitool

// AuthType 认证类型
type AuthType string

const (
	AuthTypeNone    AuthType = "none"
	AuthTypeBearer  AuthType = "bearer"
	AuthTypeBasic   AuthType = "basic"
	AuthTypeAPIKey  AuthType = "apikey"
)

// AuthConfig 认证配置
type AuthConfig struct {
	Type     AuthType `json:"type"`
	Token    string   `json:"token,omitempty"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	Key      string   `json:"key,omitempty"`
	Location string   `json:"location,omitempty"` // header 或 query
	Name     string   `json:"name,omitempty"`     // header名或query参数名
}

// Parameter 参数定义
type Parameter struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`      // string/int/float/bool/array/object
	Required    bool        `json:"required"`
	Default     interface{} `json:"default,omitempty"`
	Description string      `json:"description"`
}

// APIToolConfig API工具配置
type APIToolConfig struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	URL         string                 `json:"url"`
	Method      string                 `json:"method"`
	Auth        *AuthConfig            `json:"auth,omitempty"`
	Headers     map[string]string      `json:"headers,omitempty"`
	Query       map[string]string      `json:"query,omitempty"`
	Body        map[string]interface{} `json:"body,omitempty"`
	BodyType    string                 `json:"bodyType,omitempty"` // json 或 form
	Timeout     int                    `json:"timeout,omitempty"`
	Parameters  []Parameter            `json:"parameters,omitempty"`
}

// DefaultTimeout 默认超时时间
const DefaultTimeout = 30

// GetTimeout 获取超时时间，未配置则返回默认值
func (c *APIToolConfig) GetTimeout() int {
	if c.Timeout <= 0 {
		return DefaultTimeout
	}
	return c.Timeout
}