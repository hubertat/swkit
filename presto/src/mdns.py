# Minimal DNS-SD browser for MicroPython (and CPython, for the simulator).
#
# MicroPython on the RP2350 has no mDNS client, so this sends a "legacy
# unicast" query (RFC 6762 s6.7): a plain DNS question sent to the mDNS
# multicast group from an ordinary port. Responders answer straight back to
# that port, so no multicast group membership is needed. swkit's responder
# (brutella/dnssd) puts SRV, TXT and A records in the same reply.

import socket
import struct

try:
    import asyncio
except ImportError:  # very old MicroPython
    import uasyncio as asyncio

from compat import ticks_ms, ticks_diff, ticks_add

MDNS_ADDR = ("224.0.0.251", 5353)
SERVICE = "_swkit._tcp.local"

T_A, T_PTR, T_TXT, T_SRV = 1, 12, 16, 33


def _encode_name(name):
    out = b""
    for label in name.strip(".").split("."):
        b = label.encode()
        out += bytes((len(b),)) + b
    return out + b"\x00"


def build_query(name, qtype, qid=0x5357):
    hdr = struct.pack("!HHHHHH", qid, 0, 1, 0, 0, 0)
    return hdr + _encode_name(name) + struct.pack("!HH", qtype, 1)


def _read_name(buf, off):
    """Returns (dotted name, offset after the name), following compression."""
    labels = []
    end = None
    hops = 0
    while True:
        if off >= len(buf):
            raise ValueError("truncated name")
        ln = buf[off]
        if ln == 0:
            off += 1
            break
        if ln & 0xC0 == 0xC0:
            if end is None:
                end = off + 2
            off = ((ln & 0x3F) << 8) | buf[off + 1]
            hops += 1
            if hops > 32:
                raise ValueError("compression loop")
            continue
        off += 1
        labels.append(bytes(buf[off:off + ln]).decode("utf-8", "replace"))
        off += ln
    return ".".join(labels), (end if end is not None else off)


def _norm(name):
    return name.rstrip(".").lower()


def parse(buf):
    """Parses a DNS message into a list of (name, type, data) records."""
    if len(buf) < 12:
        return []
    _, flags, qd, an, ns, ar = struct.unpack("!HHHHHH", buf[:12])
    if not flags & 0x8000:  # a query, not a response
        return []
    off = 12
    for _ in range(qd):
        _, off = _read_name(buf, off)
        off += 4
    recs = []
    for _ in range(an + ns + ar):
        name, off = _read_name(buf, off)
        rtype, _, _, rdlen = struct.unpack("!HHIH", buf[off:off + 10])
        off += 10
        rd = off
        off += rdlen
        if rtype == T_PTR:
            data = _read_name(buf, rd)[0]
        elif rtype == T_SRV:
            _, _, port = struct.unpack("!HHH", buf[rd:rd + 6])
            data = (_read_name(buf, rd + 6)[0], port)
        elif rtype == T_TXT:
            data = {}
            p = rd
            while p < off:
                ln = buf[p]
                item = bytes(buf[p + 1:p + 1 + ln]).decode("utf-8", "replace")
                p += 1 + ln
                if "=" in item:
                    k, v = item.split("=", 1)
                    data[k.lower()] = v
                elif item:
                    data[item.lower()] = ""
        elif rtype == T_A and rdlen == 4:
            data = ".".join(str(b) for b in buf[rd:rd + 4])
        else:
            continue
        recs.append((name, rtype, data))
    return recs


def _unescape(label):
    # DNS-SD instance names escape dots and spaces as \. and \032.
    out = ""
    i = 0
    while i < len(label):
        c = label[i]
        if c == "\\" and i + 1 < len(label):
            if label[i + 1:i + 4].isdigit() and i + 3 < len(label):
                out += chr(int(label[i + 1:i + 4]))
                i += 4
                continue
            out += label[i + 1]
            i += 2
            continue
        out += c
        i += 1
    return out


class _Collector:
    def __init__(self, service):
        self.service = _norm(service)
        self.ptr = {}   # instance fqdn -> True
        self.srv = {}   # instance -> (host, port)
        self.txt = {}   # instance -> dict
        self.a = {}     # host -> ip
        self.src = {}   # instance -> responder source ip

    def feed(self, recs, src_ip):
        for name, rtype, data in recs:
            n = _norm(name)
            if rtype == T_PTR and n == self.service:
                self.ptr[_norm(data)] = data
                self.src[_norm(data)] = src_ip
            elif rtype == T_SRV:
                self.srv[n] = (_norm(data[0]), data[1])
            elif rtype == T_TXT:
                self.txt[n] = data
            elif rtype == T_A:
                self.a.setdefault(n, data)

    def results(self):
        out = []
        suffix = "." + self.service
        for inst, raw in self.ptr.items():
            srv = self.srv.get(inst)
            if not srv:
                continue
            host, port = srv
            ip = self.a.get(host) or self.src.get(inst)
            txt = self.txt.get(inst, {})
            label = raw.rstrip(".")
            if label.lower().endswith(suffix):
                label = label[:-len(suffix)]
            out.append({
                "name": _unescape(label),
                "host": host,
                "ip": ip,
                "port": port,
                "path": txt.get("path") or "/control",
                "api": txt.get("api", ""),
                "ver": txt.get("ver", ""),
            })
        out.sort(key=lambda s: s["name"].lower())
        return out


async def _exchange(queries, collector, timeout_ms, resend_ms=700):
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        s.setblocking(False)
        start = ticks_ms()
        next_send = start
        while True:
            now = ticks_ms()
            if ticks_diff(now, start) >= timeout_ms:
                break
            if ticks_diff(now, next_send) >= 0:
                for q in queries:
                    try:
                        s.sendto(q, MDNS_ADDR)
                    except OSError:
                        pass
                next_send = ticks_add(now, resend_ms)
            try:
                buf, addr = s.recvfrom(1500)
            except OSError:
                await asyncio.sleep(0.03)
                continue
            try:
                collector.feed(parse(buf), addr[0])
            except (ValueError, IndexError):
                pass
    finally:
        s.close()


async def browse(timeout_ms=2500, service=SERVICE):
    """Finds swkit controllers. Returns a list of dicts with name, host, ip,
    port, path, api, ver; empty if none answered in time."""
    col = _Collector(service)
    await _exchange([build_query(service, T_PTR)], col, timeout_ms)
    # Instances whose reply lacked SRV/A: ask for them directly once.
    missing = [i for i in col.ptr if i not in col.srv]
    if missing:
        await _exchange([build_query(i, T_SRV) for i in missing], col, 1200)
    return col.results()


async def resolve(host, timeout_ms=1500):
    """Resolves a .local host name to an IPv4 address, or None."""
    col = _Collector(SERVICE)
    await _exchange([build_query(host, T_A)], col, timeout_ms)
    return col.a.get(_norm(host))
