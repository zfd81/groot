"""SDK 身份依赖单元测试。"""

import asyncio
import dataclasses

from fastapi import Depends, FastAPI
from fastapi.testclient import TestClient
from starlette.requests import Request

from groot_service_sdk import GrootIdentity, groot_identity


def _request(headers: dict[str, str]) -> Request:
    raw = [
        (name.lower().encode("latin-1"), value.encode("latin-1"))
        for name, value in headers.items()
    ]
    return Request({"type": "http", "method": "GET", "path": "/", "headers": raw})


def test_identity_from_headers():
    request = _request(
        {
            "X-Groot-Caller": "svc-a",
            "X-Groot-Tenant-Id": "tenant-1",
            "X-Groot-User-Id": "user-1",
            "X-Groot-Request-Id": "req-1",
            "X-Groot-Service": "my-service",
        }
    )

    identity = asyncio.run(groot_identity(request))

    assert identity == GrootIdentity(
        caller="svc-a",
        tenant_id="tenant-1",
        user_id="user-1",
        request_id="req-1",
        service="my-service",
    )


def test_identity_missing_headers_are_empty_strings():
    identity = asyncio.run(groot_identity(_request({})))

    assert identity.caller == ""
    assert identity.tenant_id == ""
    assert identity.user_id == ""
    assert identity.request_id == ""
    assert identity.service == ""


def test_identity_dependency():
    app = FastAPI()

    @app.get("/whoami")
    async def whoami(identity: GrootIdentity = Depends(groot_identity)):
        return dataclasses.asdict(identity)

    client = TestClient(app)
    response = client.get(
        "/whoami",
        headers={
            "X-Groot-Caller": "svc-b",
            "X-Groot-Tenant-Id": "tenant-2",
            "X-Groot-User-Id": "user-2",
            "X-Groot-Request-Id": "req-2",
            "X-Groot-Service": "svc-two",
        },
    )

    assert response.status_code == 200
    assert response.json() == {
        "caller": "svc-b",
        "tenant_id": "tenant-2",
        "user_id": "user-2",
        "request_id": "req-2",
        "service": "svc-two",
    }
