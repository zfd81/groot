/*
 * GrootClient 测试
 *
 * Usage:
 *   cd docs/examples/java
 *   mvn test
 */

package com.groot.client;

import com.fasterxml.jackson.databind.JsonNode;
import okhttp3.mockwebserver.MockResponse;
import okhttp3.mockwebserver.MockWebServer;
import org.junit.jupiter.api.*;

import java.io.IOException;
import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.*;

@TestMethodOrder(MethodOrderer.MethodName.class)
class GrootClientTest {

    private static MockWebServer server;
    private static GrootClient client;

    private static final String SSE_EVENTS =
            "data: {\"role\":\"assistant\",\"reasoning_content\":\"让我思考一下...\"}\n\n" +
            "data: {\"role\":\"assistant\",\"content\":\"分析结果\"}\n\n" +
            "data: {\"role\":\"assistant\",\"finish_reason\":\"stop\"}\n\n" +
            "data: {\"status\":\"success\",\"result\":\"分析完成\"}\n\n" +
            "data: [DONE]\n\n";

    @BeforeAll
    static void setUp() throws IOException {
        server = new MockWebServer();
        server.start();
        client = new GrootClient("http://localhost:" + server.getPort(), "test-key");
    }

    @AfterAll
    static void tearDown() throws IOException {
        if (client != null) client.close();
        if (server != null) server.shutdown();
    }

    @Test
    void testHealthCheck() throws Exception {
        server.enqueue(new MockResponse()
                .setBody("{\"status\":\"healthy\",\"version\":\"1.0.0\"}")
                .addHeader("Content-Type", "application/json"));

        JsonNode resp = client.healthCheck();
        assertEquals("healthy", resp.get("status").asText());
        assertEquals("1.0.0", resp.get("version").asText());
    }

    @Test
    void testListSkills() throws Exception {
        server.enqueue(new MockResponse()
                .setBody("{\"skills\":[{\"name\":\"test_skill\"}],\"total\":1}")
                .addHeader("Content-Type", "application/json"));

        JsonNode resp = client.listSkills();
        assertEquals(1, resp.get("total").asInt());
        assertEquals("test_skill", resp.get("skills").get(0).get("name").asText());
    }

    @Test
    void testListTools() throws Exception {
        server.enqueue(new MockResponse()
                .setBody("{\"test_mcp\":{\"tools\":[{\"name\":\"echo\"}],\"total\":1}}")
                .addHeader("Content-Type", "application/json"));

        JsonNode resp = client.listTools();
        assertTrue(resp.has("test_mcp"));
        assertEquals("echo", resp.get("test_mcp").get("tools").get(0).get("name").asText());
    }

    @Test
    void testExecuteChatNewSession() throws Exception {
        server.enqueue(new MockResponse()
                .setBody(SSE_EVENTS)
                .addHeader("Content-Type", "text/event-stream")
                .addHeader("X-Session-ID", "sid_test_001")
                .addHeader("X-Chat-ID", "chat_test_001"));

        List<String[]> events = new ArrayList<>();
        GrootClient.ChatResult result = client.executeChat(
                "测试指令", null, null, null, null, (t, d) -> events.add(new String[]{t, d}));

        assertEquals("sid_test_001", result.getSessionId());
        assertEquals("chat_test_001", result.getChatId());
        assertEquals("success", result.getStatus());
        assertEquals("分析完成", result.getResult());

        // 验证事件类型
        boolean hasMessage = events.stream().anyMatch(e -> "message".equals(e[0]));
        assertTrue(hasMessage);
    }

    @Test
    void testExecuteChatWithSession() throws Exception {
        server.enqueue(new MockResponse()
                .setBody(SSE_EVENTS)
                .addHeader("Content-Type", "text/event-stream")
                .addHeader("X-Session-ID", "existing_sid")
                .addHeader("X-Chat-ID", "chat_test_002"));

        GrootClient.ChatResult result = client.executeChat(
                "继续分析", "existing_sid", null, null, null, null);

        assertEquals("existing_sid", result.getSessionId());
    }

    @Test
    void testExecuteChatWithPrompt() throws Exception {
        server.enqueue(new MockResponse()
                .setBody(SSE_EVENTS)
                .addHeader("Content-Type", "text/event-stream")
                .addHeader("X-Session-ID", "sid_prompt")
                .addHeader("X-Chat-ID", "chat_prompt"));

        GrootClient.ChatResult result = client.executeChat(
                "指令", null, "你是专家", null, null, null);

        assertNotNull(result.getSessionId());
    }

    @Test
    void testExecuteChatNoCallback() throws Exception {
        server.enqueue(new MockResponse()
                .setBody(SSE_EVENTS)
                .addHeader("Content-Type", "text/event-stream")
                .addHeader("X-Session-ID", "sid_nocb")
                .addHeader("X-Chat-ID", "chat_nocb"));

        GrootClient.ChatResult result = client.executeChat("简单指令", null);

        assertEquals("success", result.getStatus());
        assertEquals("分析完成", result.getResult());
    }

    @Test
    void testExecuteChatWithAttachments() throws Exception {
        server.enqueue(new MockResponse()
                .setBody(SSE_EVENTS)
                .addHeader("Content-Type", "text/event-stream")
                .addHeader("X-Session-ID", "sid_att")
                .addHeader("X-Chat-ID", "chat_att"));

        List<GrootClient.Attachment> atts = new ArrayList<>();
        atts.add(new GrootClient.Attachment("file", "test.pdf", "base64content"));

        GrootClient.ChatResult result = client.executeChat(
                "分析文件", null, null, null, atts, null);

        assertNotNull(result.getSessionId());
    }

    @Test
    void testCancelChat() throws Exception {
        server.enqueue(new MockResponse()
                .setBody("{\"status\":\"success\",\"session_id\":\"sid_x\",\"message\":\"对话已取消\"}")
                .addHeader("Content-Type", "application/json"));

        JsonNode resp = client.cancelChat("sid_x");
        assertEquals("success", resp.get("status").asText());
    }

    @Test
    void testGetChatStatus() throws Exception {
        server.enqueue(new MockResponse()
                .setBody("{\"status\":\"success\",\"chat\":null}")
                .addHeader("Content-Type", "application/json"));

        JsonNode resp = client.getChatStatus("sid_test");
        assertEquals("success", resp.get("status").asText());
    }

    @Test
    void testGetChatDetail() throws Exception {
        server.enqueue(new MockResponse()
                .setBody("{\"status\":\"success\",\"chat\":{\"chat_id\":\"cid_test\",\"status\":\"completed\"}}")
                .addHeader("Content-Type", "application/json"));

        JsonNode resp = client.getChatDetail("sid_test", "cid_test");
        assertEquals("success", resp.get("status").asText());
        assertEquals("completed", resp.get("chat").get("status").asText());
    }

    @Test
    void testGetSessionDetail() throws Exception {
        server.enqueue(new MockResponse()
                .setBody("{\"status\":\"success\",\"session\":{},\"history\":{\"messages\":[]}}")
                .addHeader("Content-Type", "application/json"));

        JsonNode resp = client.getSessionDetail("sid_test");
        assertEquals("success", resp.get("status").asText());
    }

    @Test
    void testListSessions() throws Exception {
        server.enqueue(new MockResponse()
                .setBody("{\"status\":\"success\",\"total\":0,\"sessions\":[]}")
                .addHeader("Content-Type", "application/json"));

        JsonNode resp = client.listSessions(10, 0);
        assertEquals("success", resp.get("status").asText());
    }

    @Test
    void testListSessionsPagination() throws Exception {
        server.enqueue(new MockResponse()
                .setBody("{\"status\":\"success\",\"total\":50,\"limit\":5,\"offset\":10,\"sessions\":[]}")
                .addHeader("Content-Type", "application/json"));

        JsonNode resp = client.listSessions(5, 10);
        assertEquals("success", resp.get("status").asText());
        assertEquals(5, resp.get("limit").asInt());
    }

    @Test
    void testExecuteChatHttpError() {
        server.enqueue(new MockResponse().setResponseCode(500).setBody("internal error"));

        assertThrows(IOException.class, () -> client.executeChat("测试", null));
    }

    @Test
    void testClassifyEvent() throws Exception {
        com.fasterxml.jackson.databind.ObjectMapper mapper =
                new com.fasterxml.jackson.databind.ObjectMapper();

        assertEquals("thinking",
                GrootClient.classifyEvent(mapper.readTree("{\"role\":\"assistant\",\"reasoning_content\":\"x\"}")));
        assertEquals("message",
                GrootClient.classifyEvent(mapper.readTree("{\"role\":\"assistant\",\"content\":\"x\"}")));
        assertEquals("tool_calls",
                GrootClient.classifyEvent(mapper.readTree("{\"role\":\"assistant\",\"tool_calls\":[]}")));
        assertEquals("finish",
                GrootClient.classifyEvent(mapper.readTree("{\"role\":\"assistant\",\"finish_reason\":\"stop\"}")));
        assertEquals("tool_result",
                GrootClient.classifyEvent(mapper.readTree("{\"role\":\"tool\",\"content\":\"x\"}")));
        assertEquals("completed",
                GrootClient.classifyEvent(mapper.readTree("{\"status\":\"success\"}")));
    }
}
