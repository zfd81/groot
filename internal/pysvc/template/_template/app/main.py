"""子服务模板：复制本目录并改名，即得到一个新的子服务。"""

import logging

from fastapi import File, UploadFile

log = logging.getLogger(__name__)  # 记录器名自带子服务名

METHODS = ["POST"]  # 可选；接口接受的 HTTP 方法，缺省 ["POST"]


async def startup() -> None:  # 可选；进程启动时调用一次，加载模型等重资源
    log.info("子服务启动")


async def shutdown() -> None:  # 可选；进程停止时调用一次，释放资源
    log.info("子服务停止")


async def handle(file: UploadFile = File(...)) -> dict:  # 必须；本子服务唯一的处理函数
    return {"ok": True}
