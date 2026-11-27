"""插件模板：复制本目录并改名，即得到一个新的插件。"""

import logging

from fastapi import File, UploadFile

log = logging.getLogger(__name__)  # 记录器名自带插件名

METHODS = ["POST"]  # 可选；接口接受的 HTTP 方法，缺省 ["POST"]


async def startup() -> None:  # 可选；进程启动时调用一次，加载模型等重资源
    log.info("插件启动")


async def shutdown() -> None:  # 可选；进程停止时调用一次，释放资源
    log.info("插件停止")


async def handle(file: UploadFile = File(...)) -> dict:  # 必须；本插件唯一的处理函数
    return {"ok": True}
