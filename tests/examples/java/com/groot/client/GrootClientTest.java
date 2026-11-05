/*
 * GrootClient 测试（OpenAI 官方 Java SDK 驱动 MockWebServer）。
 *
 * 被测对象是 examples/java 下的 com.groot.client.GrootClient —— 一个基于
 * com.openai:openai-java 的示例客户端。本测试验证 Groot 的 OpenAI 兼容契约：
 *
 *   1. 请求走 POST /chat/completions，且路径里没有 /v1 前缀
 *   2. 认证走 Authorization: Bearer <key>（不再有 X-API-Key 之类的自定义头）
 *   3. 扩展字段 session_id / agent_name / user 出现在请求体顶层
 *   4. 非流式响应顶层的 session_id 能被客户端读到
 *   5. 流式每个 chunk 顶层的 session_id 能被客户端读到
 *   6. HTTP 400 错误体抛出 com.openai.errors.BadRequestException，且能取到 message
 *
 * Usage:
 *   cd examples/java
 *   mvn test
 */

package com.groot.client;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.openai.errors.BadRequestException;
import okhttp3.mockwebserver.MockResponse;
import okhttp3.mockwebserver.MockWebServer;
import okhttp3.mockwebserver.RecordedRequest;
import org.junit.jupiter.api.*;

import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.*;

@TestMethodOrder(MethodOrderer.MethodName.class)
class GrootClientTest {

    private static MockWebServer server;
    private static GrootClient client;

    /** 一条 chat.completion.chunk；session_id 放在顶层（Groot 扩展字段）。 */
    private static String chunk(String deltaJson, String finishReason, String sessionId) {
        String fr = finishReason == null ? "null" : "\"" + finishReason + "\"";
        return "{\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"created\":1,"
                + "\"model\":\"test-model\",\"session_id\":\"" + sessionId + "\","
                + "\"choices\":[{\"index\":0,\"delta\":" + deltaJson
                + ",\"finish_reason\":" + fr + "}]}";
    }

    /** SSE 流：角色分片 + 两段文本 + stop，末尾 data: [DONE]。 */
    private static String sse(String sessionId) {
        return "data: " + chunk("{\"role\":\"assistant\"}", null, sessionId) + "\n\n"
                + "data: " + chunk("{\"content\":\"你好\"}", null, sessionId) + "\n\n"
                + "data: " + chunk("{\"content\":\"，世界\"}", null, sessionId) + "\n\n"
                + "data: " + chunk("{}", "stop", sessionId) + "\n\n"
                + "data: [DONE]\n\n";
    }

    /** 非流式 chat.completion；session_id 放在顶层（Groot 扩展字段）。 */
    private static String completion(String content, String sessionId) {
        return "{\"id\":\"chatcmpl-test\",\"object\":\"chat.completion\",\"created\":1,"
                + "\"model\":\"test-model\",\"session_id\":\"" + sessionId + "\","
                + "\"choices\":[{\"index\":0,\"message\":{\"role\":\"assistant\",\"content\":\""
                + content + "\"},\"finish_reason\":\"stop\"}],"
                + "\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":3,\"total_tokens\":8}}";
    }

    @BeforeAll
    static void setUp() throws IOException {
        server = new MockWebServer();
        server.start();
        client = new GrootClient("http://127.0.0.1:" + server.getPort(), "test-key", "alice");
    }

    @AfterAll
    static void tearDown() throws IOException {
        if (client != null) client.close();
        if (server != null) server.shutdown();
    }

    @BeforeEach
    void drainRequests() throws InterruptedException {
        while (server.takeRequest(0, TimeUnit.MILLISECONDS) != null) {
            // 丢弃上一个用例积压的请求
        }
    }

    private static void enqueueJson(int code, String body) {
        server.enqueue(new MockResponse()
                .setResponseCode(code)
                .setBody(body)
                .addHeader("Content-Type", "application/json"));
    }

    private RecordedRequest takeRequest() throws InterruptedException {
        RecordedRequest req = server.takeRequest(5, TimeUnit.SECONDS);
        assertNotNull(req, "客户端没有发出请求");
        return req;
    }

    /** 用 SDK 自带的 Jackson 解析请求体，避免为测试额外引入 JSON 库。 */
    private static JsonNode readBody(RecordedRequest req) throws IOException {
        return new ObjectMapper().readTree(req.getBody().readUtf8());
    }

    // ==================== 请求形状 ====================

    @Test
    void testRequestPathAndAuthHeader() throws Exception {
        enqueueJson(200, completion("你好", "sess_001"));

        client.chat("test-model", "你好", null, null);

        RecordedRequest req = takeRequest();
        assertEquals("POST", req.getMethod());
        assertEquals("/chat/completions", req.getPath());
        assertFalse(req.getPath().contains("/v1"), "路径不得包含 /v1 前缀");
        assertEquals("Bearer test-key", req.getHeader("Authorization"));
        // 旧自定义头一律不再出现
        assertNull(req.getHeader("X-API-Key"));
        assertNull(req.getHeader("X-Session-ID"));
        assertNull(req.getHeader("X-Chat-ID"));
    }

    @Test
    void testExtensionFieldsInRequestBodyTopLevel() throws Exception {
        enqueueJson(200, completion("ok", "sess_existing"));

        client.chat("test-model", "你好", "sess_existing", "analyst");

        JsonNode body = readBody(takeRequest());
        assertEquals("test-model", body.get("model").asText());
        assertEquals("你好", body.get("messages").get(0).get("content").asText());
        // 扩展字段在请求体顶层，而不是 header
        assertEquals("sess_existing", body.get("session_id").asText());
        assertEquals("analyst", body.get("agent_name").asText());
        assertEquals("alice", body.get("user").asText());
    }

    @Test
    void testExtensionFieldsOmittedWhenAbsent() throws Exception {
        enqueueJson(200, completion("ok", "sess_001"));

        // 不带 user 的客户端
        try (GrootClient bare = new GrootClient("http://127.0.0.1:" + server.getPort(), "test-key")) {
            bare.chat("test-model", "你好", null, null);
        }

        JsonNode body = readBody(takeRequest());
        assertFalse(body.has("session_id"));
        assertFalse(body.has("agent_name"));
        assertFalse(body.has("user"));
    }

    // ==================== 非流式 ====================

    @Test
    void testChatReadsTopLevelSessionId() {
        enqueueJson(200, completion("分析结果", "sess_new"));

        GrootClient.ChatResult result = client.chat("test-model", "分析数据", null, "analyst");

        assertEquals("分析结果", result.getContent());
        assertEquals("sess_new", result.getSessionId());
        assertEquals("test-model", result.getModel());
    }

    @Test
    void testListModels() throws Exception {
        enqueueJson(200, "{\"object\":\"list\",\"data\":["
                + "{\"id\":\"qwen-max\",\"object\":\"model\",\"created\":1,\"owned_by\":\"groot\"}]}");

        List<String> models = client.listModels();
        assertEquals(1, models.size());
        assertEquals("qwen-max", models.get(0));

        RecordedRequest req = takeRequest();
        assertEquals("/models", req.getPath());
        assertEquals("Bearer test-key", req.getHeader("Authorization"));
    }

    // ==================== 流式 ====================

    @Test
    void testChatStreamCollectsTextAndSessionId() throws Exception {
        server.enqueue(new MockResponse()
                .setBody(sse("sess_stream"))
                .addHeader("Content-Type", "text/event-stream"));

        final List<String> deltas = new ArrayList<>();
        final List<String> sessionIds = new ArrayList<>();
        final String[] full = new String[1];

        client.chatStream("test-model", "打个招呼", null, null, new GrootClient.StreamListener() {
            @Override
            public void onDelta(String delta) { deltas.add(delta); }

            @Override
            public void onSessionId(String sessionId) { sessionIds.add(sessionId); }

            @Override
            public void onComplete(String fullText) { full[0] = fullText; }
        });

        assertEquals("你好，世界", full[0]);
        assertEquals("你好，世界", String.join("", deltas));
        assertFalse(sessionIds.isEmpty(), "应当从 chunk 顶层读到 session_id");
        assertEquals("sess_stream", sessionIds.get(0));

        JsonNode body = readBody(takeRequest());
        assertTrue(body.get("stream").asBoolean());
        assertEquals("alice", body.get("user").asText());
    }

    @Test
    void testChatStreamPassesSessionIdInBody() throws Exception {
        server.enqueue(new MockResponse()
                .setBody(sse("sess_existing"))
                .addHeader("Content-Type", "text/event-stream"));

        client.chatStream("test-model", "继续", "sess_existing", "analyst",
                new GrootClient.StreamListener() {
                    @Override
                    public void onDelta(String delta) { }

                    @Override
                    public void onSessionId(String sessionId) { }

                    @Override
                    public void onComplete(String fullText) { }
                });

        JsonNode body = readBody(takeRequest());
        assertEquals("sess_existing", body.get("session_id").asText());
        assertEquals("analyst", body.get("agent_name").asText());
    }

    // ==================== 错误路径 ====================

    @Test
    void testBadRequestRaisesBadRequestException() {
        enqueueJson(400, "{\"error\":{\"message\":\"messages 不能为空\","
                + "\"type\":\"invalid_request_error\",\"param\":\"messages\",\"code\":null}}");

        BadRequestException ex = assertThrows(BadRequestException.class,
                () -> client.chat("test-model", "", null, null));

        assertEquals(400, ex.statusCode());
        // 官方 SDK 的 getMessage() 形如 "<status>: <error.message>"
        assertTrue(ex.getMessage().contains("messages 不能为空"), ex.getMessage());
        assertEquals("invalid_request_error", ex.type().orElse(null));
        assertEquals("messages", ex.param().orElse(null));
        // 原始错误体就是 OpenAI 的 error 对象本身（{"code","message","param","type"}）
        Object errBody = ex.body().convert(Object.class);
        assertTrue(errBody instanceof java.util.Map, String.valueOf(errBody));
        java.util.Map<?, ?> errMap = (java.util.Map<?, ?>) errBody;
        assertEquals("messages 不能为空", String.valueOf(errMap.get("message")));
        assertEquals("invalid_request_error", String.valueOf(errMap.get("type")));
    }
}
