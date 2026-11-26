"""Groot Python 服务进程启动器。

启动器持有整个进程唯一的 FastAPI 应用：按宿主给定的清单加载各子服务的
``app/main.py``，把其中的 ``handle`` 注册到 ``/<子服务名>``，校验内部令牌，
并在就绪后向 stdout 输出一行结构化就绪消息。子服务作者不调用本模块。
"""

from __future__ import annotations

import argparse
import contextlib
import hmac
import importlib
import importlib.util
import inspect
import json
import logging
import os
import re
import socket
import sys
import threading
import time
import types
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import uvicorn
from fastapi import Depends, FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from starlette.exceptions import HTTPException as StarletteHTTPException

from . import SDK_VERSION
from .errors import APIError, error_envelope

log = logging.getLogger(__name__)

READY_PREFIX = "GROOT_SERVICE_READY "
TOKEN_HEADER = b"x-groot-internal-token"
HEALTH_PATH = "/healthz"
# 与 Go 侧 discover 一致：healthz 是健康端点的保留名，不能作为子服务名。
RESERVED_NAME = "healthz"

# 子服务目录名规则，与宿主 internal/pysvc 的发现规则一致。
_NAME_RE = re.compile(r"^[a-z0-9][a-z0-9_-]*$")
_REQUIRED_FILES = ("app/__init__.py", "app/main.py", "requirements.txt")
# 校验错误 loc 中的容器前缀，取 param 时跳过。
_LOC_CONTAINERS = frozenset({"body", "query", "path", "header", "cookie"})
# 导入失败时 METHODS 未知，占位路径接受全部常用方法，统一返回 503。
_UNAVAILABLE_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"]


@dataclass
class LoadedService:
    """一个已加载的子服务及其入口函数。"""

    name: str
    methods: list[str]
    handle: Callable[..., Any]
    startup: Callable[[], Any] | None = None
    shutdown: Callable[[], Any] | None = None
    ready: bool = False


async def maybe_await(value: Any) -> Any:
    """兼容协程与普通函数的返回值：可等待则等待，否则原样返回。"""
    if inspect.isawaitable(value):
        return await value
    return value


def _ensure_namespace(name: str) -> types.ModuleType:
    """在 sys.modules 中登记一个空的命名空间包，供子服务包挂载。"""
    module = sys.modules.get(name)
    if module is None:
        module = types.ModuleType(name)
        module.__path__ = []  # type: ignore[attr-defined]
        sys.modules[name] = module
    return module


def load_service(services_root: Path, name: str) -> LoadedService:
    """加载 ``<services_root>/<name>/app`` 包并读取其入口契约。

    ``app/`` 被注册为 ``groot_services.<name>.app``，因此子服务内部可使用相对
    导入，且不同子服务的 ``app`` 包互不冲突。缺少 ``handle`` 或导入失败时抛出
    异常，由调用方记录并跳过该子服务。
    """
    root = Path(services_root)
    app_dir = root / name / "app"
    init_file = app_dir / "__init__.py"
    if not init_file.is_file():
        raise FileNotFoundError(f"子服务 {name} 缺少 {init_file}")

    module_name = name.replace("-", "_")
    app_name = f"groot_services.{module_name}.app"
    root_module = _ensure_namespace("groot_services")
    parent_module = _ensure_namespace(f"groot_services.{module_name}")
    setattr(root_module, module_name, parent_module)

    # 丢弃此前加载的同名模块，保证每次加载都拿到干净的子服务状态。
    for key in [k for k in sys.modules if k == app_name or k.startswith(app_name + ".")]:
        del sys.modules[key]

    spec = importlib.util.spec_from_file_location(
        app_name, init_file, submodule_search_locations=[str(app_dir)]
    )
    if spec is None or spec.loader is None:
        raise ImportError(f"无法加载子服务 {name} 的 app 包")
    app_module = importlib.util.module_from_spec(spec)
    sys.modules[app_name] = app_module
    setattr(parent_module, "app", app_module)
    spec.loader.exec_module(app_module)

    main_module = importlib.import_module(f"{app_name}.main")
    handle = getattr(main_module, "handle", None)
    if not callable(handle):
        raise ImportError(f"子服务 {name} 的 app/main.py 未定义可调用对象 handle")
    methods = getattr(main_module, "METHODS", None) or ["POST"]
    return LoadedService(
        name=name,
        methods=[str(method) for method in methods],
        handle=handle,
        startup=getattr(main_module, "startup", None),
        shutdown=getattr(main_module, "shutdown", None),
    )


def _unavailable_handler(name: str) -> Callable[[], Awaitable[None]]:
    """构造导入失败子服务的占位处理器：任何请求都返回 503 信封。"""

    async def unavailable() -> None:
        raise APIError(
            503,
            f"子服务 {name} 加载失败",
            type="api_error",
            code="service_unavailable",
        )

    return unavailable


def _ready_gate(service: LoadedService) -> Callable[[], Awaitable[None]]:
    """构造就绪依赖：子服务未就绪时返回 503 信封。

    作为 FastAPI 依赖挂载，而不是包装 ``handle``，从而保留真实签名供
    FastAPI 注入文件、表单、查询参数与 ``Depends``。
    """

    async def require_ready() -> None:
        if not service.ready:
            raise APIError(
                503,
                f"子服务 {service.name} 未就绪",
                type="api_error",
                code="service_unavailable",
            )

    return require_ready


def _make_lifespan(
    services: list[LoadedService], results: dict[str, str]
) -> Callable[[FastAPI], Any]:
    """构造应用 lifespan：按名顺序启动、逆序停止，单个失败不影响其余子服务。"""

    @contextlib.asynccontextmanager
    async def lifespan(_app: FastAPI) -> Any:
        started: list[LoadedService] = []
        for service in services:
            try:
                if service.startup is not None:
                    await maybe_await(service.startup())
            except Exception as exc:  # noqa: BLE001 - 单个子服务失败必须隔离
                results[service.name] = f"error: {exc}"
                log.exception("子服务 %s 启动失败", service.name)
                continue
            service.ready = True
            results[service.name] = "ok"
            started.append(service)
            log.info("子服务 %s 已就绪", service.name)
        try:
            yield
        finally:
            for service in reversed(started):
                if service.shutdown is None:
                    continue
                try:
                    await maybe_await(service.shutdown())
                except Exception:  # noqa: BLE001 - 停止失败只记日志
                    log.exception("子服务 %s 停止失败", service.name)

    return lifespan


def _first_param(exc: RequestValidationError) -> str | None:
    """取校验错误中首个出错字段名，作为信封的 param。"""
    for error in exc.errors():
        for part in error.get("loc", ()):
            if isinstance(part, str) and part not in _LOC_CONTAINERS:
                return part
    return None


async def _handle_api_error(_request: Request, exc: APIError) -> JSONResponse:
    return JSONResponse(error_envelope(exc), status_code=exc.status_code)


async def _handle_http_exception(_request: Request, exc: StarletteHTTPException) -> JSONResponse:
    envelope = error_envelope(
        APIError(
            exc.status_code,
            str(exc.detail),
            type="invalid_request_error",
            code="invalid_request",
        )
    )
    return JSONResponse(envelope, status_code=exc.status_code)


async def _handle_validation_error(_request: Request, exc: RequestValidationError) -> JSONResponse:
    envelope = error_envelope(
        APIError(
            400,
            "请求参数校验失败",
            type="invalid_request_error",
            param=_first_param(exc),
            code="invalid_request",
        )
    )
    return JSONResponse(envelope, status_code=400)


async def _handle_unhandled(request: Request, exc: Exception) -> JSONResponse:
    log.exception("处理请求 %s 时发生未捕获异常", request.url.path)
    envelope = error_envelope(
        APIError(500, "内部错误", type="api_error", code="internal_error")
    )
    return JSONResponse(envelope, status_code=500)


class TokenMiddleware:
    """内部令牌校验：除 ``/healthz`` 外的请求都必须携带匹配的内部令牌。

    空令牌只出现在 ``--dev`` 调试模式；``read_handshake`` 保证正常启动的令牌非空。
    """

    def __init__(self, app: Any, token: str) -> None:
        self.app = app
        self.token = token

    async def __call__(self, scope: dict, receive: Callable, send: Callable) -> None:
        if scope["type"] != "http" or not self.token or scope.get("path") == HEALTH_PATH:
            await self.app(scope, receive, send)
            return
        provided = b""
        for key, value in scope.get("headers", ()):
            if key.lower() == TOKEN_HEADER:
                provided = value
                break
        if not hmac.compare_digest(provided, self.token.encode("utf-8")):
            response = JSONResponse(
                error_envelope(
                    APIError(
                        401,
                        "内部令牌无效",
                        type="authentication_error",
                        code="invalid_api_key",
                    )
                ),
                status_code=401,
            )
            await response(scope, receive, send)
            return
        await self.app(scope, receive, send)


def build_app(
    services_root: Path, names: list[str], token: str
) -> tuple[FastAPI, dict[str, str]]:
    """创建唯一的 FastAPI 应用并返回 ``(app, results)``。

    路由在启动前注册，是否可用由就绪依赖决定；``results`` 在加载失败时立即写入
    ``error: ...``，其余条目在 lifespan 启动阶段写入。
    """
    results: dict[str, str] = {}
    services: list[LoadedService] = []
    failed: list[str] = []
    for name in sorted(names):
        try:
            services.append(load_service(services_root, name))
        except Exception as exc:  # noqa: BLE001 - 单个子服务加载失败必须隔离
            results[name] = f"error: {exc}"
            failed.append(name)
            log.exception("子服务 %s 加载失败", name)

    app = FastAPI(
        lifespan=_make_lifespan(services, results),
        docs_url=None,
        redoc_url=None,
        openapi_url=None,
    )

    for service in list(services):
        try:
            app.add_api_route(
                f"/{service.name}",
                service.handle,
                methods=service.methods,
                dependencies=[Depends(_ready_gate(service))],
            )
        except Exception as exc:  # noqa: BLE001 - 路由注册失败（如参数类型无法解析）同样隔离
            results[service.name] = f"error: {exc}"
            failed.append(service.name)
            services.remove(service)  # lifespan 持有同一列表，不再启动它
            log.exception("子服务 %s 路由注册失败", service.name)

    # 导入或注册失败的子服务仍注册占位路径，让请求得到 503 而不是 404。
    for name in failed:
        app.add_api_route(f"/{name}", _unavailable_handler(name), methods=_UNAVAILABLE_METHODS)

    @app.get(HEALTH_PATH)
    async def healthz() -> dict[str, Any]:
        # 进程存活即 ok；各子服务的就绪状态来自 results（ok / error: ... / 尚未启动则缺省）。
        return {"status": "ok", "sdk": SDK_VERSION, "services": dict(results)}

    app.add_middleware(TokenMiddleware, token=token)
    app.add_exception_handler(APIError, _handle_api_error)
    app.add_exception_handler(StarletteHTTPException, _handle_http_exception)
    app.add_exception_handler(RequestValidationError, _handle_validation_error)
    app.add_exception_handler(Exception, _handle_unhandled)
    return app, results


def scan_services(root: Path) -> list[str]:
    """扫描目录下的直接子目录，返回符合契约的子服务名（升序）。"""
    names: list[str] = []
    try:
        entries = sorted(Path(root).iterdir())
    except OSError:
        return names
    for entry in entries:
        name = entry.name
        if not entry.is_dir() or name.startswith((".", "_")) or not _NAME_RE.match(name):
            continue
        if name == RESERVED_NAME:
            continue
        if all((entry / rel).is_file() for rel in _REQUIRED_FILES):
            names.append(name)
    return names


def read_handshake(dev: bool = False, only: str | None = None) -> tuple[str, list[str]]:
    """读取启动握手：正常模式读 stdin 两行，开发模式扫描当前目录并跳过令牌。"""
    if dev:
        names = scan_services(Path.cwd())
        if only is not None:
            names = [name for name in names if name == only]
        return "", names

    stream = sys.stdin.buffer
    token_line = stream.readline()
    if not token_line:
        raise SystemExit("未从 stdin 读到内部令牌")
    names_line = stream.readline()
    token = token_line.decode("utf-8").strip()
    if not token:
        raise SystemExit("内部令牌为空：非 --dev 模式必须经 stdin 第一行提供令牌")
    try:
        names = json.loads(names_line.decode("utf-8") or "[]")
    except json.JSONDecodeError as exc:
        raise SystemExit(f"子服务清单不是合法 JSON: {exc}") from exc
    if not isinstance(names, list) or not all(isinstance(name, str) for name in names):
        raise SystemExit("子服务清单必须是字符串数组")
    return token, names


def _configure_stdout() -> None:
    """把 stdout 固定为 UTF-8，避免终端编码不支持 CJK 时写日志/就绪行抛异常。"""
    stream = sys.stdout
    reconfigure = getattr(stream, "reconfigure", None)
    if reconfigure is None:
        return
    try:
        reconfigure(encoding="utf-8", errors="backslashreplace")
    except (ValueError, OSError):
        # 管道或已包装的流可能不支持 reconfigure；写就绪行时仍有兜底。
        pass


def _configure_logging() -> None:
    """把根 logger 接到 stdout，级别取 GROOT_LOG_LEVEL，格式含记录器名。"""
    level = getattr(logging, os.environ.get("GROOT_LOG_LEVEL", "INFO").upper(), logging.INFO)
    if not isinstance(level, int):
        level = logging.INFO
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(logging.Formatter("%(asctime)s %(levelname)s %(name)s: %(message)s"))
    root = logging.getLogger()
    for existing in list(root.handlers):
        root.removeHandler(existing)
    root.addHandler(handler)
    root.setLevel(level)


def _bind_socket() -> socket.socket:
    """绑定 127.0.0.1:0，端口由内核分配。"""
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    sock.bind(("127.0.0.1", 0))
    return sock


def _lifeline(server: uvicorn.Server) -> None:
    """生命线：stdin 读到 EOF 说明宿主已退出，触发优雅停止。"""
    try:
        while sys.stdin.buffer.readline():
            pass
    except Exception:  # noqa: BLE001 - 生命线任何异常都只用于触发退出
        pass
    finally:
        server.should_exit = True


def _report_ready(
    server: uvicorn.Server, port: int, results: dict[str, str], dev: bool
) -> None:
    """等待 uvicorn 完成启动后输出就绪行。"""
    while not server.started and not server.should_exit:
        time.sleep(0.01)
    if not server.started:
        return
    payload = {
        "port": port,
        "pid": os.getpid(),
        "sdk": SDK_VERSION,
        "services": dict(results),
    }
    # ensure_ascii=True 让就绪行无论 stdout 编码为何都是纯 ASCII（子服务错误原因可能含
    # CJK）；写失败只记录日志，避免就绪线程静默死亡让宿主一直等到超时。
    line = READY_PREFIX + json.dumps(payload, ensure_ascii=True) + "\n"
    try:
        sys.stdout.write(line)
        sys.stdout.flush()
    except Exception:  # noqa: BLE001 - 就绪行写失败必须留下日志
        log.exception("输出就绪行失败")
        return
    if dev:
        sys.stderr.write(f"groot_service_sdk dev server listening on 127.0.0.1:{port}\n")
        sys.stderr.flush()


def _parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="groot_service_sdk.launcher", description="Groot Python 子服务启动器"
    )
    parser.add_argument(
        "--dev", action="store_true", help="开发模式：扫描当前目录并跳过令牌校验"
    )
    parser.add_argument("--only", default=None, help="开发模式下只加载指定子服务")
    return parser.parse_args(argv)


def main() -> None:
    """进程入口：握手、构建应用、绑定环回端口、就绪上报并运行到退出。"""
    args = _parse_args()
    _configure_stdout()
    _configure_logging()
    token, names = read_handshake(dev=args.dev, only=args.only)
    app, results = build_app(Path.cwd(), names, token)

    sock = _bind_socket()
    port = sock.getsockname()[1]
    config = uvicorn.Config(
        app,
        host="127.0.0.1",
        port=port,
        lifespan="on",
        log_config=None,
        access_log=False,
    )
    server = uvicorn.Server(config)

    if not args.dev:
        threading.Thread(
            target=_lifeline, args=(server,), name="groot-lifeline", daemon=True
        ).start()
    threading.Thread(
        target=_report_ready,
        args=(server, port, results, args.dev),
        name="groot-ready",
        daemon=True,
    ).start()

    try:
        server.run(sockets=[sock])
    finally:
        sock.close()


if __name__ == "__main__":
    main()
