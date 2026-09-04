// 后端 API 响应类型定义，字段名对齐 groot 服务实际返回。

export interface SessionSummary {
  session_id: string
  created_at: string
  round_count: number
  last_active_at: string
  // 首轮用户指令，作为列表标题展示；旧版后端或无对话记录时为空
  title?: string
  path: string
}

export interface SessionHistoryResp {
  status: string
  total: number
  limit: number
  offset: number
  sessions: SessionSummary[]
}

export interface HistoryMessage {
  round: number
  chat_id: string
  timestamp: string
  instruction: string
  result: string
  status: string
  duration: number
  steps_count: number
  agent_name: string
  error: { code: string; message: string } | null
}

export interface SessionDetailResp {
  status: string
  session_id: string
  session: Record<string, unknown>
  history: {
    session_id: string
    created_at: string
    messages: HistoryMessage[]
  }
}

export interface ChatStep {
  step_id: string
  type: string
  name: string
  start_time: string
  end_time: string
  status: string
  nesting_level: number
  error: string
}

export interface ChatRecord {
  chat_id: string
  session_id: string
  round: number
  prompt: string
  timestamp: string
  started_at: string
  ended_at: string
  instruction: string
  result: string
  status: string
  duration: string
  duration_ms: number
  caller: string
  steps: ChatStep[]
  agent_name: string
  model: string
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  error: { code: string; message: string } | null
}

export interface ModelInfo {
  name: string
  model: string
  base_url: string
  api_key: string // 脱敏后的展示值
  max_completion_tokens: number
  max_context_tokens: number
  temperature: number
  top_p: number
  frequency_penalty: number
  presence_penalty: number
  seed: number
  stop: string[]
  thinking: boolean
  is_default: boolean
  enabled: boolean
}

// 创建/更新模型的请求体（api_key 为空表示更新时保持原值）
export interface ModelForm {
  name: string
  model: string
  base_url: string
  api_key: string
  max_completion_tokens: number
  max_context_tokens: number
  temperature: number
  top_p: number
  frequency_penalty: number
  presence_penalty: number
  seed: number
  stop: string[]
  thinking: boolean
  enabled: boolean
}

export interface ModelTestResp {
  status: string // healthy | unhealthy
  message: string
}

export interface ModelsResp {
  models: ModelInfo[]
  default: string
  total: number
}

export interface HealthResp {
  status: string
  version: string
  uptime: string
  checks: {
    llm: { status: string; info: { model: string; error: string } }
    mcp_servers: {
      status: string
      info: Array<{
        name: string
        type: string
        description: string
        isActive: boolean
        tools_count: number
        error: string
      }>
    }
    skills: { status: string; info: { count: number } }
    memory: Record<string, unknown>
    // 运行环境信息（设置-通用面板展示）；旧版后端可能缺失
    environment?: {
      status: string
      info: { home_dir: string; database: string; log_dir: string }
    }
  }
}

export interface MeResp {
  authenticated: boolean
  auth_required: boolean
  needs_setup: boolean
  username?: string
}

export interface SkillInfo {
  name: string
  description: string
}

export interface SkillsResp {
  skills: SkillInfo[]
  total: number
}

export interface ToolInfo {
  name: string
  description: string
}

export interface ToolsGroup {
  // MCP 定义中的 type / description；合成分组（_builtin）无这两个字段
  type?: string
  description?: string
  tools: ToolInfo[]
  total: number
}

// /tools 返回 { groupName: {tools, total} }
export type ToolsResp = Record<string, ToolsGroup>

export interface AgentInfo {
  name: string
  description: string
  skills: SkillInfo[]
}

export interface AgentsResp {
  agents: AgentInfo[]
}

// /web/agents/:name/definition 返回 Agent 定义文件原文
export interface AgentDefinitionResp {
  name: string
  file: string
  content: string
}

// API Key 管理（/web/apikeys）
export interface ApiKeyInfo {
  id: string
  name: string
  permissions: string[]
  expires_at: number
  created_at: number
  expired: boolean
}

export interface ApiKeysResp {
  keys: ApiKeyInfo[]
  total: number
}

export interface ApiKeyCreateResp extends ApiKeyInfo {
  token: string
}

export interface ApiKeyTokenResp {
  token: string
}

// 集群成员（/web/cluster）：address 为 IP:PORT，时间字段为毫秒时间戳
export interface ClusterMemberInfo {
  reg_id: string
  role: string
  address: string
  pid: number
  heartbeat_at: number
  created_at: number
}

export interface ClusterResp {
  members: ClusterMemberInfo[]
}
