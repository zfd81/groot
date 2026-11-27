"""SDK 错误信封单元测试。"""

from groot_plugin_sdk import APIError, error_envelope


def test_envelope_shape():
    exc = APIError(400, "bad", type="invalid_request_error", code="unsupported_type")

    assert exc.status_code == 400
    assert exc.message == "bad"
    assert exc.type == "invalid_request_error"
    assert exc.param is None
    assert exc.code == "unsupported_type"

    assert error_envelope(exc) == {
        "error": {
            "message": "bad",
            "type": "invalid_request_error",
            "param": None,
            "code": "unsupported_type",
        }
    }
