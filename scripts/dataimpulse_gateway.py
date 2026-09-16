#!/usr/bin/env python3
"""Independent, loopback-only SOCKS5 facade for three DataImpulse sticky exits.

The provider owns session lifetime. This service checks observed public IPs,
selects distinct sessions and fails closed on an unsuccessful/duplicate sample.
It cannot promise that a residential provider never changes an IP between probes.
No sbmgr imports, account API, billing API, or credential logging are used.
"""
from __future__ import annotations

import argparse
import asyncio
import contextlib
import hmac
import ipaddress
import json
import os
from pathlib import Path
import secrets
import signal
import struct
import time


def unique_ghana(samples):
    return (len(samples) == 3 and all(s and s.get("country") == "GH" for s in samples)
            and len({s["ip"] for s in samples if s}) == 3)


async def address(reader, kind):
    if kind == 1:
        return await reader.readexactly(4)
    if kind == 4:
        return await reader.readexactly(16)
    if kind == 3:
        size = await reader.readexactly(1)
        if not size[0]:
            raise ValueError("empty target")
        return size + await reader.readexactly(size[0])
    raise ValueError("unsupported address")


class Gateway:
    def __init__(self, config):
        self.config = config
        self.login = config["login"] + "__cr.gh;sessttl.120"
        self.password = config["password"]
        self.local_password = config["local_password"]
        for value in (self.login, self.password, self.local_password):
            if not value or len(value.encode()) > 255 or any(c in value for c in "\r\n\x00"):
                raise ValueError("invalid credential shape")
        self.ports = config.get("listen_ports", [31001, 31002, 31003])
        if len(self.ports) != 3 or len(set(self.ports)) != 3 or any(not 1024 <= p <= 65535 for p in self.ports):
            raise ValueError("three distinct unprivileged local ports required")
        self.active = [None] * 3
        self.connections = [set() for _ in range(3)]
        self.capacity = asyncio.Semaphore(256)
        self.next_port = 10003
        self.state_path = Path(config.get("status_file", "/srv/dataimpulse-gateway/status.json"))

    async def probe(self, port):
        def quoted(value):
            return value.replace("\\", "\\\\").replace('"', '\\"')
        config = 'proxy-user = "' + quoted(self.login + ":" + self.password) + '"\n'
        process = await asyncio.create_subprocess_exec(
            "curl", "-q", "--config", "-", "--proxy", f"socks5h://gw.dataimpulse.com:{port}",
            "--silent", "--fail", "--connect-timeout", "10", "--max-time", "20",
            "--max-filesize", "8192", "https://api.country.is/",
            stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL)
        try:
            raw, _ = await asyncio.wait_for(process.communicate(config.encode()), 23)
            if process.returncode:
                return None
            sample = json.loads(raw)
            if not ipaddress.ip_address(sample["ip"]).is_global or sample.get("country") != "GH":
                return None
            return {"port": port, "ip": sample["ip"], "country": "GH"}
        except (TimeoutError, ValueError, KeyError):
            if process.returncode is None:
                process.kill()
                await process.wait()
            return None

    def publish(self, selected, reason):
        for slot, (old, new) in enumerate(zip(self.active, selected)):
            if old != new:
                for writer in tuple(self.connections[slot]):
                    writer.close()
        self.active = list(selected)
        status = {"sampled_at": time.time(), "ttl_minutes": 120, "country": "GH",
                  "ready": unique_ghana(selected), "reason": reason,
                  "sessions": selected, "guarantee": "distinct at successful probes; provider may rotate between probes"}
        temporary = self.state_path.with_name(self.state_path.name + ".tmp")
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(status, stream)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, self.state_path)
        print(f"ready={int(status['ready'])} distinct={3 if status['ready'] else 0} reason={reason}", flush=True)

    async def maintain(self):
        ports = [10000, 10001, 10002]
        while True:
            samples = await asyncio.gather(*(self.probe(p) for p in ports))
            if not unique_ghana(samples):
                self.publish([None] * 3, "sample-unavailable-or-duplicate")
                used = set()
                for i, sample in enumerate(samples):
                    if sample and sample["ip"] not in used:
                        used.add(sample["ip"])
                        continue
                    samples[i] = None
                    for _ in range(10):
                        candidate = self.next_port
                        self.next_port = 10000 + (self.next_port - 9999) % 10001
                        if candidate in ports:
                            continue
                        found = await self.probe(candidate)
                        if found and found["ip"] not in used:
                            samples[i] = found
                            ports[i] = candidate
                            used.add(found["ip"])
                            break
                # Re-sample selected sessions together, not only a series of
                # historic candidates. Failed samples never count as unique.
                samples = await asyncio.gather(*(self.probe(p) for p in ports))
            if unique_ghana(samples):
                self.publish(samples, "verified")
            else:
                self.publish([None] * 3, "three-distinct-exits-unavailable")
            await asyncio.sleep(60)

    async def upstream(self, port, request):
        reader, writer = await asyncio.open_connection("gw.dataimpulse.com", port)
        try:
            writer.write(b"\x05\x01\x02")
            await writer.drain()
            if await reader.readexactly(2) != b"\x05\x02":
                raise ValueError("upstream authentication unavailable")
            login, password = self.login.encode(), self.password.encode()
            writer.write(b"\x01" + bytes([len(login)]) + login + bytes([len(password)]) + password)
            await writer.drain()
            if await reader.readexactly(2) != b"\x01\x00":
                raise ValueError("upstream authentication failed")
            writer.write(request)
            await writer.drain()
            header = await reader.readexactly(4)
            if header[:3] != b"\x05\x00\x00":
                raise ValueError("upstream target failed")
            await address(reader, header[3])
            await reader.readexactly(2)
            return reader, writer
        except BaseException:
            writer.close()
            raise

    async def serve(self, reader, writer, slot):
        upstream_writer = None
        self.connections[slot].add(writer)
        try:
            async with self.capacity:
                async with asyncio.timeout(25):
                    header = await reader.readexactly(2)
                    if header[0] != 5 or not header[1]:
                        return
                    methods = await reader.readexactly(header[1])
                    if 2 not in methods:
                        writer.write(b"\x05\xff")
                        await writer.drain()
                        return
                    writer.write(b"\x05\x02")
                    await writer.drain()
                    auth = await reader.readexactly(2)
                    user = await reader.readexactly(auth[1])
                    size = (await reader.readexactly(1))[0]
                    password = await reader.readexactly(size)
                    valid = auth[0] == 1 and hmac.compare_digest(user, b"sbmgr") and hmac.compare_digest(password, self.local_password.encode())
                    writer.write(b"\x01" + (b"\x00" if valid else b"\x01"))
                    await writer.drain()
                    if not valid:
                        return
                    header = await reader.readexactly(4)
                    if header[:3] != b"\x05\x01\x00":
                        writer.write(b"\x05\x07\x00\x01" + b"\x00" * 6)
                        await writer.drain()
                        return
                    request = header + await address(reader, header[3]) + await reader.readexactly(2)
                    selected = self.active[slot]
                    if selected is None:
                        raise ValueError("no verified session")
                    upstream_reader, upstream_writer = await self.upstream(selected["port"], request)
                    if self.active[slot] != selected:
                        raise ValueError("session changed during connection")
                    writer.write(b"\x05\x00\x00\x01" + b"\x00" * 6)
                    await writer.drain()

                async def copy(source, destination):
                    while data := await asyncio.wait_for(source.read(65536), 600):
                        destination.write(data)
                        await destination.drain()
                    if destination.can_write_eof():
                        destination.write_eof()

                jobs = [asyncio.create_task(copy(reader, upstream_writer)), asyncio.create_task(copy(upstream_reader, writer))]
                try:
                    await asyncio.gather(*jobs)
                finally:
                    for job in jobs:
                        job.cancel()
                    await asyncio.gather(*jobs, return_exceptions=True)
        except (OSError, ValueError, TimeoutError, asyncio.IncompleteReadError):
            # Never echo proxy responses, target URLs or credentials.
            pass
        finally:
            self.connections[slot].discard(writer)
            writer.close()
            if upstream_writer:
                upstream_writer.close()

    async def run(self):
        servers = []
        try:
            for slot, port in enumerate(self.ports):
                servers.append(await asyncio.start_server(lambda r, w, i=slot: self.serve(r, w, i), "127.0.0.1", port))
            await self.maintain()
        finally:
            for server in servers:
                server.close()
                await server.wait_closed()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True)
    args = parser.parse_args()
    os.umask(0o077)
    try:
        config = json.loads(Path(args.config).read_text(encoding="utf-8"))
        asyncio.run(Gateway(config).run())
    except KeyboardInterrupt:
        pass
    except Exception:
        raise SystemExit("gateway stopped: configuration or runtime failure; credentials suppressed")


if __name__ == "__main__":
    main()
