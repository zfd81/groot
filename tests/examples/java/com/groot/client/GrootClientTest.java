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
import okhttp3.mockwebserver.RecordedRequest;
import org.junit.jupiter.api.*;

import java.io.File;
import java.io.IOException;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.*;

@TestMethodOrder(MethodOrderer.MethodName.class)
class GrootClientTest {

    private static MockWebServer server;
    private static GrootClient client;

    private static final String CHUNK_HEAD =
            "{\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,";

    /** OpenAI chat.completion.chunk 格式的 SSE 流：thinking / message / tool_result / finish(stop) / [DONE] */
    private static final String SSE_EVENTS =
            "data: " + CHUNK_HEAD + "\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"让我思考一下...\"},\"finish_reason\":null}]}\n\n" +
            "data: " + CHUNK_HEAD + "\"delta\":{\"role\":\"assistant\",\"content\":\"分析结果\"},\"finish_reason\":null}]}\n\n" +
            "data: " + CHUNK_HEAD + "\"delta\":{\"role\":\"tool\",\"tool_call_id\":\"call_1\",\"tool_name\":\"skill\",\"content\":\"ok\",\"error\":false},\"finish_reason\":null}]}\n\n" +
            "data: " + CHUNK_HEAD + "\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
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
        // 正常结束的流不含 error 事件，status / result 均为 null
        assertNull(result.getStatus());
        assertNull(result.getResult());
        assertNull(result.getError());

        // 验证事件类型序列（已摊平）
        List<String> types = new ArrayList<>();
        for (String[] e : events) types.add(e[0]);
        assertEquals(Arrays.asList("thinking", "message", "tool_result", "finish"), types);

        // 回调收到的是摊平后的 JSON，而不是原始 chunk
        JsonNode first = new com.fasterxml.jackson.databind.ObjectMapper().readTree(events.get(0)[1]);
        assertFalse(first.has("choices"));
        assertEquals("assistant", first.get("role").asText());
        assertEquals("让我思考一下...", first.get("reasoning_content").asText());

        JsonNode toolRes = new com.fasterxml.jackson.databind.ObjectMapper().readTree(events.get(2)[1]);
        assertEquals("call_1", toolRes.get("tool_call_id").asText());
        assertEquals("skill", toolRes.get("tool_name").asText());

        JsonNode finish = new com.fasterxml.jackson.databind.ObjectMapper().readTree(events.get(3)[1]);
        assertEquals("stop", finish.get("finish_reason").asText());
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

        assertEquals("sid_nocb", result.getSessionId());
        assertNull(result.getStatus());
        assertNull(result.getResult());
    }

    @Test
    void testExecuteChatErrorEvent() throws Exception {
        String body =
                "data: " + CHUNK_HEAD + "\"delta\":{\"role\":\"assistant\",\"content\":\"部分\"},\"finish_reason\":null}]}\n\n" +
                "data: {\"error\":{\"message\":\"boom\",\"type\":\"server_error\",\"code\":null,\"param\":null}}\n\n" +
                "data: [DONE]\n\n";
        server.enqueue(new MockResponse()
                .setBody(body)
                .addHeader("Content-Type", "text/event-stream")
                .addHeader("X-Session-ID", "sid_err")
                .addHeader("X-Chat-ID", "chat_err"));

        List<String[]> events = new ArrayList<>();
        GrootClient.ChatResult result = client.executeChat(
                "出错指令", null, null, null, null, (t, d) -> events.add(new String[]{t, d}));

        assertEquals("error", result.getStatus());
        assertEquals("boom", result.getError());
        assertEquals(2, events.size());
        assertEquals("message", events.get(0)[0]);
        assertEquals("error", events.get(1)[0]);

        JsonNode err = new com.fasterxml.jackson.databind.ObjectMapper().readTree(events.get(1)[1]);
        assertEquals("error", err.get("event").asText());
        assertEquals("boom", err.get("message").asText());
        assertEquals("server_error", err.get("error").get("type").asText());
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

    /** 构造 choices[0].delta = deltaJson、finish_reason = finishReason 的 chunk 字符串。 */
    private static String chunk(String deltaJson, String finishReason) {
        String fr = finishReason == null ? "null" : "\"" + finishReason + "\"";
        return CHUNK_HEAD + "\"delta\":" + deltaJson + ",\"finish_reason\":" + fr + "}]}";
    }

    @Test
    void testClassifyEvent() throws Exception {
        com.fasterxml.jackson.databind.ObjectMapper mapper =
                new com.fasterxml.jackson.databind.ObjectMapper();

        assertEquals("thinking", GrootClient.classifyEvent(GrootClient.flattenChunk(
                mapper.readTree(chunk("{\"role\":\"assistant\",\"reasoning_content\":\"x\"}", null)))));
        assertEquals("message", GrootClient.classifyEvent(GrootClient.flattenChunk(
                mapper.readTree(chunk("{\"role\":\"assistant\",\"content\":\"x\"}", null)))));
        assertEquals("tool_calls", GrootClient.classifyEvent(GrootClient.flattenChunk(
                mapper.readTree(chunk("{\"role\":\"assistant\",\"tool_calls\":[]}", null)))));
        assertEquals("finish", GrootClient.classifyEvent(GrootClient.flattenChunk(
                mapper.readTree(chunk("{}", "stop")))));
        assertEquals("tool_result", GrootClient.classifyEvent(GrootClient.flattenChunk(
                mapper.readTree(chunk("{\"role\":\"tool\",\"tool_call_id\":\"c1\",\"content\":\"x\"}", null)))));
        assertEquals("error", GrootClient.classifyEvent(GrootClient.flattenChunk(
                mapper.readTree("{\"error\":{\"message\":\"boom\"}}"))));
        // 没有 choices 也没有 error 的对象原样透传，无法归类
        assertEquals("unknown", GrootClient.classifyEvent(GrootClient.flattenChunk(
                mapper.readTree("{\"foo\":\"bar\"}"))));
    }

    @Test
    void testFlattenChunkFinish() throws Exception {
        com.fasterxml.jackson.databind.ObjectMapper mapper =
                new com.fasterxml.jackson.databind.ObjectMapper();

        JsonNode flat = GrootClient.flattenChunk(mapper.readTree(chunk("{}", "stop")));
        assertTrue(flat.isObject());
        assertEquals(1, flat.size());
        assertEquals("stop", flat.get("finish_reason").asText());
    }

    @Test
    void testFlattenChunkError() throws Exception {
        com.fasterxml.jackson.databind.ObjectMapper mapper =
                new com.fasterxml.jackson.databind.ObjectMapper();

        JsonNode flat = GrootClient.flattenChunk(mapper.readTree(
                "{\"error\":{\"message\":\"boom\",\"type\":\"server_error\",\"code\":null,\"param\":null}}"));
        assertEquals("error", flat.get("event").asText());
        assertEquals("boom", flat.get("message").asText());
        assertEquals("server_error", flat.get("error").get("type").asText());

        // error 不是对象时 message 为空串
        JsonNode flat2 = GrootClient.flattenChunk(mapper.readTree("{\"error\":\"oops\"}"));
        assertEquals("error", flat2.get("event").asText());
        assertEquals("", flat2.get("message").asText());
        assertEquals("oops", flat2.get("error").asText());
    }

    @Test
    void testFlattenChunkDeltaNotMutated() throws Exception {
        com.fasterxml.jackson.databind.ObjectMapper mapper =
                new com.fasterxml.jackson.databind.ObjectMapper();

        JsonNode raw = mapper.readTree(chunk("{\"role\":\"assistant\",\"content\":\"hi\"}", "stop"));
        JsonNode flat = GrootClient.flattenChunk(raw);
        assertEquals("hi", flat.get("content").asText());
        assertEquals("stop", flat.get("finish_reason").asText());
        // 原始 delta 是深拷贝来源，不应被写入 finish_reason
        assertFalse(raw.get("choices").get(0).get("delta").has("finish_reason"));
    }

    /**
     * 其他用例只 enqueue 响应、不取走 RecordedRequest，共享的 MockWebServer 会积压请求；
     * 需要断言请求体的用例先把积压清空，保证 takeRequest() 拿到的是本用例的请求。
     */
    private static void drainRequests() throws InterruptedException {
        while (server.takeRequest(0, TimeUnit.MILLISECONDS) != null) {
            // 丢弃
        }
    }

    @Test
    void testTranscribe() throws Exception {
        drainRequests();
        server.enqueue(new MockResponse()
                .setBody("{\"text\":\"你好，世界\",\"model\":\"my-asr\"}")
                .addHeader("Content-Type", "application/json"));

        File audio = Files.createTempFile("rec", ".webm").toFile();
        audio.deleteOnExit();
        Files.write(audio.toPath(), "FAKE-AUDIO".getBytes());

        JsonNode resp = client.transcribe(audio, "my-asr", "zh");
        assertEquals("你好，世界", resp.get("text").asText());
        assertEquals("my-asr", resp.get("model").asText());

        RecordedRequest req = server.takeRequest();
        assertEquals("/audio/transcriptions", req.getPath());
        assertTrue(req.getHeader("Content-Type").startsWith("multipart/form-data"));
        String body = req.getBody().readUtf8();
        assertTrue(body.contains("name=\"file\""));
        assertTrue(body.contains("name=\"model\""));
        assertTrue(body.contains("name=\"language\""));
    }

    @Test
    void testTranscribeWithoutModel() throws Exception {
        drainRequests();
        server.enqueue(new MockResponse()
                .setBody("{\"text\":\"ok\",\"model\":\"whisper-1\"}")
                .addHeader("Content-Type", "application/json"));

        File audio = Files.createTempFile("rec", ".webm").toFile();
        audio.deleteOnExit();
        Files.write(audio.toPath(), "x".getBytes());

        client.transcribe(audio, null, null);

        // 不传 model / language 时表单里不应出现这两个字段，交由服务端回落
        String body = server.takeRequest().getBody().readUtf8();
        assertFalse(body.contains("name=\"model\""));
        assertFalse(body.contains("name=\"language\""));
    }
}
