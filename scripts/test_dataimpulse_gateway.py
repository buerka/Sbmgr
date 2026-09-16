import asyncio
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("gateway", Path(__file__).with_name("dataimpulse_gateway.py"))
gateway = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gateway)


class GatewayTests(unittest.TestCase):
    def test_failed_or_duplicate_samples_never_count_as_unique(self):
        good = [{"ip": "192.0.2." + str(i), "country": "GH"} for i in range(1, 4)]
        self.assertTrue(gateway.unique_ghana(good))
        self.assertFalse(gateway.unique_ghana([good[0], good[0], good[2]]))
        self.assertFalse(gateway.unique_ghana([good[0], None, good[2]]))
        self.assertFalse(gateway.unique_ghana([good[0], {"ip": "192.0.2.2", "country": "US"}, good[2]]))

    def test_config_requires_three_distinct_local_ports(self):
        with self.assertRaises(ValueError):
            gateway.Gateway({"login": "fake", "password": "fake", "local_password": "fake", "listen_ports": [31001] * 3})

    def test_local_authentication_and_udp_rejection(self):
        async def exercise():
            proxy = gateway.Gateway({"login": "fake", "password": "fake", "local_password": "test-only"})
            server = await asyncio.start_server(lambda r, w: proxy.serve(r, w, 0), "127.0.0.1", 0)
            try:
                r, w = await asyncio.open_connection("127.0.0.1", server.sockets[0].getsockname()[1])
                w.write(b"\x05\x01\x02")
                await w.drain()
                self.assertEqual(await r.readexactly(2), b"\x05\x02")
                w.write(b"\x01\x05sbmgr\x09test-only")
                await w.drain()
                self.assertEqual(await r.readexactly(2), b"\x01\x00")
                w.write(b"\x05\x03\x00\x01")
                await w.drain()
                self.assertEqual((await r.readexactly(10))[:2], b"\x05\x07")
                w.close()
                await w.wait_closed()
            finally:
                server.close()
                await server.wait_closed()
        asyncio.run(exercise())


if __name__ == "__main__":
    unittest.main()
