package agent

// MainAgentName 是主 Agent 的名字。
// 启动期扫描 subagents/ 时若发现同名目录会跳过并报错（保留主名独占）。
// 事件循环按 event.AgentName == MainAgentName 区分主/子 Agent 事件。
const MainAgentName = "groot"
