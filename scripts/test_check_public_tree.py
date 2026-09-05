"""Regression checks for the public-tree privacy gate; no real credentials."""
import unittest
import check_public_tree as check


class PrivacyGateTests(unittest.TestCase):
    def test_example_suffix_does_not_exempt_credentials(self):
        for name in ("server.example.key", "client.example.pem", "state.example.db"):
            self.assertIsNotNone(check.forbidden_path_reason(name))

    def test_examples_are_still_content_scanned(self):
        marker = "-----BEGIN " + "PRIVATE KEY-----"
        self.assertTrue(check.scan_content("config.example.json", marker.encode()))

    def test_binary_is_not_silently_skipped(self):
        self.assertTrue(check.scan_content("innocent.txt", b"header\x00payload"))

    def test_documented_example_is_allowed(self):
        self.assertIsNone(check.forbidden_path_reason("config.example.json"))
        self.assertEqual(check.scan_content("config.example.json", b'{"server":"proxy.example"}'), [])


if __name__ == "__main__":
    unittest.main()
