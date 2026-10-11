# Tiny non-blocking HTTP/1.0 client on asyncio streams.
#
# urequests blocks the whole interpreter for the duration of a request,
# which would freeze touch handling and animation on every poll. This one
# yields while waiting on the network, so the UI keeps running.
#
# HTTP/1.0 keeps it simple: the server answers without chunked encoding and
# closes the connection, so the body is everything up to EOF.

import json

try:
    import asyncio
except ImportError:
    import uasyncio as asyncio


class HttpError(Exception):
    def __init__(self, status, text):
        super().__init__(text or ("HTTP %d" % status))
        self.status = status


async def _request(host, port, method, path, body, max_body):
    reader, writer = await asyncio.open_connection(host, port)
    try:
        payload = b""
        if body is not None:
            payload = json.dumps(body).encode()
        head = "%s %s HTTP/1.0\r\nHost: %s:%d\r\nAccept: application/json\r\nUser-Agent: swkit-presto\r\n" % (
            method, path, host, port)
        if body is not None:
            head += "Content-Type: application/json\r\nContent-Length: %d\r\n" % len(payload)
        writer.write(head.encode() + b"\r\n" + payload)
        await writer.drain()

        status_line = await reader.readline()
        parts = status_line.split(None, 2)
        if len(parts) < 2:
            raise OSError("bad response")
        status = int(parts[1])
        while True:
            line = await reader.readline()
            if not line or line == b"\r\n":
                break
        chunks = []
        size = 0
        while True:
            chunk = await reader.read(2048)
            if not chunk:
                break
            size += len(chunk)
            if size > max_body:
                raise OSError("response too large")
            chunks.append(chunk)
        data = b"".join(chunks)
    finally:
        try:
            writer.close()
            await writer.wait_closed()
        except Exception:
            pass
    if status >= 400:
        raise HttpError(status, data.decode().strip()[:120])
    return json.loads(data) if data else None


async def request(host, port, method, path, body=None, timeout=5.0, max_body=96 * 1024):
    """Sends a request and returns the decoded JSON body. Raises HttpError
    on an HTTP error status, asyncio.TimeoutError on timeout, and OSError
    when the controller cannot be reached."""
    return await asyncio.wait_for(_request(host, port, method, path, body, max_body), timeout)
