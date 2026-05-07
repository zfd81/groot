/*
 * Groot AI Agent Java 客户端
 *
 * Usage:
 *   GrootClient client = new GrootClient("http://localhost:8080", "your-api-key");
 *
 *   // 新建会话
 *   GrootClient.ChatResult result = client.executeChat("帮我分析数据", (type, data) -> {
 *       System.out.println("[" + type + "] " + data);
 *   });
 *   System.out.println("会话ID: " + result.getSessionId());
 *
 *   // 继续会话（多轮对话）
 *   GrootClient.ChatResult result2 = client.executeChat("生成图表", result.getSessionId(), null);
 */

package com.groot.client;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import okhttp3.*;

import java.io.IOException;
import java.util.*;
import java.util.concurrent.TimeUnit;
import java.util.function.BiConsumer;

public class GrootClient implements AutoCloseable {

    private static final MediaType JSON = MediaType.get("application/json; charset=utf-8");
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final String baseUrl;
    private final OkHttpClient httpClient;

    /**
     * 创建客户端实例。
     *
     * @param baseUrl Groot 服务地址，如 "http://localhost:8080"
     * @param apiKey  API 密钥（可为 null，对应未启用认证的场景）
     */
    public GrootClient(String baseUrl, String apiKey) {
        this.baseUrl = baseUrl.replaceAll("/$", "");

        OkHttpClient.Builder builder = new OkHttpClient.Builder()
                .connectTimeout(30, TimeUnit.SECONDS)
                .readTimeout(0, TimeUnit.SECONDS);  // SSE 长连接，不设读超时

        if (apiKey != null && !apiKey.isEmpty()) {
            builder.addInterceptor(chain -> {
                Request req = chain.request().newBuilder()
                        .header("X-API-Key", apiKey)
                        .build();
                return chain.proceed(req);
            });
        }

        this.httpClient = builder.build();
    }

    // ==================== 数据模型 ====================

    /** 对话执行结果 */
    public static class ChatResult {
        private String sessionId;
        private String chatId;
        private String status;
        private String result;

        public String getSessionId() { return sessionId; }
        public void setSessionId(String sessionId) { this.sessionId = sessionId; }
        public String getChatId() { return chatId; }
        public void setChatId(String chatId) { this.chatId = chatId; }
        public String getStatus() { return status; }
        public void setStatus(String status) { this.status = status; }
        public String getResult() { return result; }
        public void setResult(String result) { this.result = result; }
    }

    /** 附件 */
    public static class Attachment {
        private String type;
        private String name;
        private String content;

        public Attachment(String type, String name, String content) {
            this.type = type;
            this.name = name;
            this.content = content;
        }

        public String getType() { return type; }
        public String getName() { return name; }
        public String getContent() { return content; }
    }

    // ==================== 核心接口 ====================

    /**
     * 执行对话（SSE 流式返回）。
     *
     * @param instruction 用户任务指令
     * @param callback    事件回调 callback(eventType, jsonString)
     * @return ChatResult 包含 sessionId、chatId、status 等
     */
    public ChatResult executeChat(String instruction,
                                   BiConsumer<String, String> callback) throws IOException {
        return executeChat(instruction, null, null, null, null, callback);
    }

    /**
     * 执行对话（SSE 流式返回）—— 完整参数版本。
     *
     * @param instruction 用户指令
     * @param sessionId   会话 ID（null 则创建新会话）
     * @param prompt      系统提示词（可选）
     * @param modelName   模型名称（可选）
     * @param attachments 附件列表（可选）
     * @param callback    事件回调
     */
    public ChatResult executeChat(String instruction,
                                   String sessionId,
                                   String prompt,
                                   String modelName,
                                   List<Attachment> attachments,
                                   BiConsumer<String, String> callback) throws IOException {

        // 构建请求体
        Map<String, Object> body = new LinkedHashMap<>();
        body.put("instruction", instruction);
        if (prompt != null) body.put("prompt", prompt);
        if (attachments != null) {
            List<Map<String, String>> list = new ArrayList<>();
            for (Attachment a : attachments) {
                Map<String, String> m = new LinkedHashMap<>();
                m.put("type", a.getType());
                m.put("name", a.getName());
                m.put("content", a.getContent());
                list.add(m);
            }
            body.put("attachments", list);
        }

        Request.Builder reqBuilder = new Request.Builder()
                .url(baseUrl + "/chat")
                .post(RequestBody.create(MAPPER.writeValueAsString(body), JSON));

        if (sessionId != null) reqBuilder.header("X-Session-ID", sessionId);
        if (modelName != null) reqBuilder.header("X-Model-Name", modelName);

        Request request = reqBuilder.build();
        Response response = httpClient.newCall(request).execute();

        if (!response.isSuccessful()) {
            String err = response.body() != null ? response.body().string() : "";
            response.close();
            throw new IOException("HTTP " + response.code() + ": " + err);
        }

        ChatResult result = new ChatResult();
        result.setSessionId(response.header("X-Session-ID"));
        result.setChatId(response.header("X-Chat-ID"));

        // 逐行读取 SSE
        String line;
        while ((line = response.body().source().readUtf8Line()) != null) {
            if (!line.startsWith("data:")) continue;
            String data = line.substring(5).trim();
            if ("[DONE]".equals(data)) break;

            JsonNode parsed;
            try {
                parsed = MAPPER.readTree(data);
            } catch (JsonProcessingException e) {
                continue;
            }

            String eventType = classifyEvent(parsed);
            if (callback != null) callback.accept(eventType, data);

            if ("completed".equals(eventType)) {
                result.setStatus(parsed.has("status") ? parsed.get("status").asText() : null);
                if ("success".equals(result.getStatus()) && parsed.has("result")) {
                    result.setResult(parsed.get("result").asText());
                }
            } else if ("error".equals(eventType)) {
                result.setStatus("error");
            }
        }

        response.close();
        return result;
    }

    /** 取消指定会话中正在执行的对话。 */
    public JsonNode cancelChat(String sessionId) throws IOException {
        Request req = new Request.Builder()
                .url(baseUrl + "/chat/" + sessionId)
                .delete()
                .build();
        return executeJson(req);
    }

    /** 查询指定会话最近一次对话的运行状态。 */
    public JsonNode getChatStatus(String sessionId) throws IOException {
        Request req = new Request.Builder()
                .url(baseUrl + "/chat/status/" + sessionId)
                .get()
                .build();
        return executeJson(req);
    }

    /** 查询指定会话中某次对话的完整详情。 */
    public JsonNode getChatDetail(String sessionId, String chatId) throws IOException {
        Request req = new Request.Builder()
                .url(baseUrl + "/chat/" + sessionId + "/" + chatId)
                .get()
                .build();
        return executeJson(req);
    }

    /** 查询会话详情（包含完整对话历史）。 */
    public JsonNode getSessionDetail(String sessionId) throws IOException {
        Request req = new Request.Builder()
                .url(baseUrl + "/sess/" + sessionId)
                .get()
                .build();
        return executeJson(req);
    }

    /** 分页查询会话列表。 */
    public JsonNode listSessions(int limit, int offset) throws IOException {
        HttpUrl url = HttpUrl.parse(baseUrl + "/sess/history").newBuilder()
                .addQueryParameter("limit", String.valueOf(limit))
                .addQueryParameter("offset", String.valueOf(offset))
                .build();
        Request req = new Request.Builder().url(url).get().build();
        return executeJson(req);
    }

    // ==================== 查询接口 ====================

    /** 健康检查。 */
    public JsonNode healthCheck() throws IOException {
        Request req = new Request.Builder().url(baseUrl + "/health").get().build();
        return executeJson(req);
    }

    /** 列出可用 Skills。 */
    public JsonNode listSkills() throws IOException {
        Request req = new Request.Builder().url(baseUrl + "/skills").get().build();
        return executeJson(req);
    }

    /** 列出可用 MCP 工具。 */
    public JsonNode listTools() throws IOException {
        Request req = new Request.Builder().url(baseUrl + "/tools").get().build();
        return executeJson(req);
    }

    // ==================== 内部方法 ====================

    @Override
    public void close() {
        httpClient.dispatcher().executorService().shutdown();
        httpClient.connectionPool().evictAll();
    }

    private JsonNode executeJson(Request request) throws IOException {
        try (Response response = httpClient.newCall(request).execute()) {
            if (!response.isSuccessful()) {
                String err = response.body() != null ? response.body().string() : "";
                throw new IOException("HTTP " + response.code() + ": " + err);
            }
            ResponseBody body = response.body();
            if (body == null) throw new IOException("empty response body");
            return MAPPER.readTree(body.string());
        }
    }

    static String classifyEvent(JsonNode data) {
        if (!data.isObject()) return "unknown";
        JsonNode role = data.get("role");
        if (role != null && "tool".equals(role.asText())) return "tool_result";
        if (role != null && "assistant".equals(role.asText())) {
            if (data.has("tool_calls")) return "tool_calls";
            if (data.has("finish_reason")) return "finish";
            if (data.has("reasoning_content")) return "thinking";
            if (data.has("content")) return "message";
        }
        if (data.has("status")) return "completed";
        return "unknown";
    }
}
