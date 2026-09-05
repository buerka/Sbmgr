#!/usr/bin/env python3
"""Reject common deployment data and privacy leaks in repository candidates."""

from __future__ import annotations

import ipaddress
import re
import subprocess
import sys
from pathlib import Path, PurePosixPath


ROOT = Path(__file__).resolve().parents[1]

FORBIDDEN_BASENAMES = {
    "authorized_keys",
    "audit.jsonl",
    "audit.previous.jsonl",
    "config.base.json",
    "id_ed25519",
    "id_ecdsa",
    "id_dsa",
    "id_rsa",
    "known_hosts",
    "mihomo.template.yaml",
    "sing-box.json",
    "state.db",
    "state.json",
    "state.json.lock",
    "state.json.migrated",
    "state.lock",
}

FORBIDDEN_SUFFIXES = {
    ".cer",
    ".crt",
    ".db",
    ".db-shm",
    ".db-wal",
    ".db-journal",
    ".jsonl",
    ".key",
    ".log",
    ".mobileconfig",
    ".ovpn",
    ".p12",
    ".pem",
    ".pfx",
    ".ppk",
    ".secret",
    ".sqlite",
    ".sqlite-shm",
    ".sqlite-wal",
    ".sqlite-journal",
    ".sqlite3",
    ".sqlite3-shm",
    ".sqlite3-wal",
    ".sqlite3-journal",
}

FORBIDDEN_DIRS = {
    ".drafts",
    ".private",
    ".secrets",
    ".ssh",
    "backups",
    "certbot-venv",
    "exports",
    "letsencrypt",
    "logs",
}

CONTENT_RULES = (
    (
        "private-key block",
        re.compile(r"-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----"),
    ),
    (
        "personal Windows path",
        re.compile(r"(?i)\b[A-Z]:[\\/](?:Users|Documents and Settings)[\\/]"),
    ),
    (
        "privileged remote-login endpoint",
        re.compile(
            r"(?i)(?:\bssh\b[^\r\n]{0,200}\b(?:root|admin)@"
            r"[A-Za-z0-9.-]+(?::[0-9]{1,5})?|\b(?:root|admin)@"
            r"(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?::[0-9]{1,5})?)"
        ),
    ),
    (
        "credential embedded in URL",
        re.compile(r"(?i)https?://[^\s/:@]+:[^\s/@]+@"),
    ),
    (
        "literal subscription token",
        re.compile(r"/(?:sub|qr)/[A-Za-z0-9_-]{20,}(?:\.png)?"),
    ),
)

IPV4_PATTERN = re.compile(r"(?<![0-9])(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?![0-9])")
DOCUMENTATION_NETWORKS = tuple(
    ipaddress.ip_network(cidr)
    for cidr in ("192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24")
)


def repository_files() -> list[str]:
    output = subprocess.check_output(
        ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"],
        cwd=ROOT,
        stderr=subprocess.STDOUT,
    )
    return [item.decode("utf-8", "surrogateescape") for item in output.split(b"\0") if item]


def forbidden_path_reason(relative: str) -> str | None:
    path = PurePosixPath(relative)
    lower_parts = tuple(part.lower() for part in path.parts)
    basename = lower_parts[-1]
    example_file = ".example." in basename

    if basename in FORBIDDEN_BASENAMES:
        return "runtime or credential filename"
    if basename.startswith("state.db-"):
        return "SQLite state sidecar"
    if basename.startswith("credentials") and basename.endswith(".json"):
        return "credential bundle"
    if not example_file and len(lower_parts) == 1 and basename.endswith((".json", ".yaml", ".yml")):
        if basename.startswith(("config", "mihomo", "sing-box")) or "subscription" in basename:
            return "runtime configuration"
    if any(part in FORBIDDEN_DIRS or part.startswith("letsencrypt") for part in lower_parts[:-1]):
        return "runtime or credential directory"
    if any(basename.endswith(suffix) for suffix in FORBIDDEN_SUFFIXES):
        return "runtime or credential file type"
    if basename.startswith(".env") and basename != ".env.example":
        return "environment file"
    return None


def is_unexpected_public_ipv4(value: str) -> bool:
    try:
        address = ipaddress.ip_address(value)
    except ValueError:
        return False
    if not isinstance(address, ipaddress.IPv4Address) or not address.is_global:
        return False
    return not any(address in network for network in DOCUMENTATION_NETWORKS)


def scan_content(relative: str, raw: bytes) -> list[tuple[int, str]]:
    if b"\0" in raw:
        return [(1, "unexpected binary content; review and explicitly allow a safe source asset")]
    text = raw.decode("utf-8", "replace")
    findings: list[tuple[int, str]] = []
    for line_number, line in enumerate(text.splitlines(), 1):
        for description, pattern in CONTENT_RULES:
            if pattern.search(line):
                findings.append((line_number, description))
        if any(is_unexpected_public_ipv4(value) for value in IPV4_PATTERN.findall(line)):
            findings.append((line_number, "non-documentation public IPv4 address"))
    return findings


def main() -> int:
    findings: list[str] = []
    try:
        paths = repository_files()
    except (OSError, subprocess.CalledProcessError) as error:
        print(f"repository privacy check could not list candidate files: {error}", file=sys.stderr)
        return 2

    for relative in paths:
        path_reason = forbidden_path_reason(relative)
        if path_reason:
            findings.append(f"{relative}: {path_reason}")
            continue

        path = ROOT / Path(relative)
        try:
            raw = path.read_bytes()
        except OSError as error:
            findings.append(f"{relative}: unreadable tracked file ({error.__class__.__name__})")
            continue
        for line_number, description in scan_content(relative, raw):
            findings.append(f"{relative}:{line_number}: {description}")

    if findings:
        print("Repository privacy check failed:", file=sys.stderr)
        for finding in findings:
            print(f"- {finding}", file=sys.stderr)
        print("No matched values were printed. Review the referenced tracked files.", file=sys.stderr)
        return 1

    print(f"Repository privacy check passed ({len(paths)} candidate files).")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
