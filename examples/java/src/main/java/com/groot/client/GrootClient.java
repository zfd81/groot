/*
 * Groot AI Agent Java 示例（OpenAI 官方 Java SDK）。
 *
 * Groot 对外提供 OpenAI 兼容的 REST 接口，因此客户端直接使用官方
 * com.openai:openai-java，**只需替换 baseUrl 与 apiKey**：
 *
 *   - 接口路径：POST /chat/completions（没有 /v1 前缀，baseUrl 也不带 /v1）
 *   - 认证方式：Authorization: Bearer <apiKey>
 *   - 模型列表：GET /models
 *
 * 在 OpenAI 标准字段之外，Groot 额外支持三个扩展字段，放在**请求体顶层**
 * （通过 SDK 的 putAdditionalBodyProperty 透传）：
 *
 *   session_id  会话 ID。不传则创建新会话；服务端在响应体顶层返回它，
 *               后续请求带上同一个 session_id 即为多轮对话。
 *   agent_name  指定使用的智能体名称，省略时使用默认智能体。
 *   user        业务用户标识（1~64 个字符，字母/数字/下划线/短横线），
 *               用于会话归属与隔离。
 *
 * 用法：
 *   mvn -q package
 *   java -cp target/classes:<依赖> com.groot.client.GrootClient
 * 或直接看 {@link #main(String[])} 里的调用。
 */

package com.groot.client;

import com.openai.client.OpenAIClient;
import com.openai.client.okhttp.OpenAIOkHttpClient;
import com.openai.core.JsonValue;
import com.openai.core.http.StreamResponse;
import com.openai.models.chat.completions.ChatCompletion;
import com.openai.models.chat.completions.ChatCompletionChunk;
import com.openai.models.chat.completions.ChatCompletionCreateParams;
import com.openai.models.models.Model;

/**
 * Groot 客户端：官方 {@link OpenAIClient} 的薄包装，只负责拼装 Groot 的
 * 三个扩展字段并给出读取回调。
 */
public class GrootClient implements AutoCloseable {

    private final OpenAIClient client;
    private final String user;

    /**
     * @param baseUrl Groot 服务地址，如 "http://localhost:8080"（不要带 /v1）
     * @param apiKey  Web 界面创建的 API Key，SDK 以 Bearer 方式发送
     * @param user    业务用户标识；为业务用户标识的扩展字段 user 随每个请求发送，可为 null
     */
    public GrootClient(String baseUrl, String apiKey, String user) {
        this.client = OpenAIOkHttpClient.builder()
                .baseUrl(baseUrl)
                .apiKey(apiKey)
                .build();
        this.user = user;
    }

    public GrootClient(String baseUrl, String apiKey) {
        this(baseUrl, apiKey, null);
    }

    /** 暴露底层 SDK 客户端，便于调用任意标准 OpenAI 接口。 */
    public OpenAIClient openai() {
        return client;
    }

    // ==================== 请求参数组装 ====================

    /**
     * 组装请求参数：标准字段 + Groot 扩展字段（请求体顶层）。
     * 为 null 的扩展字段不下发，由服务端使用默认值。
     */
    private ChatCompletionCreateParams.Builder params(String model, String message,
                                                      String sessionId, String agentName) {
        ChatCompletionCreateParams.Builder builder = ChatCompletionCreateParams.builder()
                .model(model)
                .addUserMessage(message);
        if (sessionId != null && !sessionId.isEmpty()) {
            builder.putAdditionalBodyProperty("session_id", JsonValue.from(sessionId));
        }
        if (agentName != null && !agentName.isEmpty()) {
            builder.putAdditionalBodyProperty("agent_name", JsonValue.from(agentName));
        }
        if (user != null && !user.isEmpty()) {
            builder.putAdditionalBodyProperty("user", JsonValue.from(user));
        }
        return builder;
    }

    // ==================== 非流式 ====================

    /** 一次性返回完整回答。 */
    public ChatResult chat(String model, String message, String sessionId, String agentName) {
        ChatCompletion resp = client.chat().completions().create(
                params(model, message, sessionId, agentName).build());
        String content = resp.choices().get(0).message().content().orElse("");
        return new ChatResult(content, extra(resp._additionalProperties(), "session_id"), resp.model());
    }

    // ==================== 流式 ====================

    /**
     * 流式返回，逐条把增量文本交给 listener；session_id 出现在每个 chunk 的顶层，
     * 拿到即通过 {@link StreamListener#onSessionId(String)} 回调（通常首条 chunk 就有）。
     */
    public void chatStream(String model, String message, String sessionId, String agentName,
                           StreamListener listener) {
        ChatCompletionCreateParams request = params(model, message, sessionId, agentName).build();
        StringBuilder text = new StringBuilder();
        try (StreamResponse<ChatCompletionChunk> stream =
                     client.chat().completions().createStreaming(request)) {
            stream.stream().forEach(chunk -> {
                String sid = extra(chunk._additionalProperties(), "session_id");
                if (sid != null) {
                    listener.onSessionId(sid);
                }
                chunk.choices().forEach(choice -> choice.delta().content().ifPresent(delta -> {
                    text.append(delta);
                    listener.onDelta(delta);
                }));
            });
        }
        listener.onComplete(text.toString());
    }

    // ==================== 模型列表（标准 OpenAI 接口）====================

    /** GET /models，返回可用模型 ID 列表。 */
    public java.util.List<String> listModels() {
        java.util.List<String> ids = new java.util.ArrayList<>();
        for (Model m : client.models().list().data()) {
            ids.add(m.id());
        }
        return ids;
    }

    @Override
    public void close() {
        client.close();
    }

    /** 从模型的扩展属性里取字符串字段；不存在时返回 null。 */
    private static String extra(java.util.Map<String, JsonValue> additional, String key) {
        JsonValue value = additional.get(key);
        if (value == null) {
            return null;
        }
        // JsonValue 继承自原始类型 JsonField，asString() 会被擦除为原始 Optional，
        // 这里用 convert 拿到强类型 String。
        return value.convert(String.class);
    }

    // ==================== 结果与回调 ====================

    /** 非流式结果。 */
    public static class ChatResult {
        private final String content;
        private final String sessionId;
        private final String model;

        public ChatResult(String content, String sessionId, String model) {
            this.content = content;
            this.sessionId = sessionId;
            this.model = model;
        }

        public String getContent() { return content; }
        /** 服务端在响应体顶层返回的会话 ID。 */
        public String getSessionId() { return sessionId; }
        public String getModel() { return model; }
    }

    /** 流式回调。 */
    public interface StreamListener {
        /** 收到一条增量文本。 */
        void onDelta(String delta);
        /** 从 chunk 顶层读到 session_id 时回调。 */
        void onSessionId(String sessionId);
        /** 全部增量拼接完成。 */
        void onComplete(String fullText);
    }

    // ==================== 演示 ====================

    public static void main(String[] args) {
        String baseUrl = System.getenv().getOrDefault("GROOT_BASE_URL", "http://localhost:8080");
        String apiKey = System.getenv().getOrDefault("GROOT_API_KEY", "your-api-key");
        String model = System.getenv().getOrDefault("GROOT_MODEL", "qwen-max");

        try (GrootClient groot = new GrootClient(baseUrl, apiKey, "alice")) {
            System.out.println("== 可用模型 ==");
            for (String id : groot.listModels()) {
                System.out.println(" - " + id);
            }

            // 1. 非流式：首次提问，服务端创建新会话并在响应顶层返回 session_id
            System.out.println("\n== 非流式 ==");
            ChatResult result = groot.chat(model, "用一句话介绍你自己", null, "analyst");
            System.out.println("回答: " + result.getContent());
            System.out.println("会话 ID: " + result.getSessionId());

            // 2. 流式：带上上一步的 session_id 继续多轮对话
            System.out.println("\n== 流式（续用同一会话）==");
            final String[] sessionId = {result.getSessionId()};
            groot.chatStream(model, "继续，再说一句", sessionId[0], "analyst",
                    new StreamListener() {
                        @Override
                        public void onDelta(String delta) {
                            System.out.print(delta);
                            System.out.flush();
                        }

                        @Override
                        public void onSessionId(String sid) {
                            sessionId[0] = sid;
                        }

                        @Override
                        public void onComplete(String fullText) {
                            System.out.println("\n会话 ID: " + sessionId[0]);
                        }
                    });
        }
    }
}
