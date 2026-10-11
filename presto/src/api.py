# swkit control API client (see server/control_server.go).

import ahttp


class Client:
    def __init__(self, host, port, path="/control"):
        self.host = host
        self.port = int(port)
        self.path = "/" + (path or "/control").strip("/")

    def label(self):
        if self.port == 80:
            return self.host
        return "%s:%d" % (self.host, self.port)

    async def get(self, sub, timeout=5.0):
        return await ahttp.request(self.host, self.port, "GET", self.path + sub, None, timeout)

    async def post(self, sub, body=None, timeout=8.0):
        return await ahttp.request(self.host, self.port, "POST", self.path + sub, body if body is not None else {}, timeout)

    async def info(self, timeout=4.0):
        """{"name", "api"} from /api/info; None on controllers too old to
        have it (they still serve the device list)."""
        try:
            return await self.get("/api/info", timeout)
        except ahttp.HttpError as e:
            if e.status == 404:
                return None
            raise
