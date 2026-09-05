package main

import (
	"net"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Only the logger's prefix may precede an inbound event. Never search inside
// the client-controlled destination for another event or identity.
const logPrefix = `^(?:(?:[+-][0-9]{4} )?(?:[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9:.]+ )?(?:TRACE|DEBUG|INFO|WARN|ERROR|FATAL) (?:\[([0-9]{1,20}) [0-9a-zµ.]+\] )?)?`
const inboundLogPrefix = logPrefix + `inbound/[a-z0-9_-]+\[[^\[\]\x00-\x1f]{1,128}\]: `

var (
	accessLogPattern = regexp.MustCompile(inboundLogPrefix + `\[([^\[\]\x00-\x1f]+)\] inbound (?:packet )?connection to (\S+)$`)
	sourceLogPattern = regexp.MustCompile(inboundLogPrefix + `inbound (?:packet )?connection from (\S+)$`)
	closeLogPattern  = regexp.MustCompile(inboundLogPrefix + `(?:connection closed|connection close|closed connection)(?:: [^\r\n]*)?$`)
)

func unsafeTextRune(r rune) bool {
	return unicode.IsControl(r) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

func safeTerminalText(s string) string {
	s = cleanLogMessage(s)
	// Strip even unterminated escape introducers; their payload becomes inert
	// printable text. This also protects views of previously persisted records.
	return strings.Map(func(r rune) rune {
		if unsafeTextRune(r) {
			return -1
		}
		return r
	}, s)
}

func safeLogMessage(s string) (string, bool) {
	s = cleanLogMessage(s)
	return s, len(s) <= 2048 && utf8.ValidString(s) && safeTerminalText(s) == s
}

// Preserve only application SGR styling and line breaks at the final terminal
// boundary; OSC/DCS/CSI and bidi controls from old records remain inert.
func safeTerminalView(s string) string {
	var b strings.Builder
	start := 0
	plain := func(v string) {
		for i, line := range strings.Split(v, "\n") {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(safeTerminalText(line))
		}
	}
	for _, loc := range ansiEscapePattern.FindAllStringIndex(s, -1) {
		plain(s[start:loc[0]])
		b.WriteString(s[loc[0]:loc[1]])
		start = loc[1]
	}
	plain(s[start:])
	return b.String()
}

func validTargetHost(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	host = strings.TrimSuffix(host, ".")
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
				return false
			}
		}
	}
	return true
}

func logAddress(address string) (string, bool) {
	host, port, err := net.SplitHostPort(address)
	p, portErr := strconv.Atoi(port)
	return host, err == nil && portErr == nil && p > 0 && p <= 65535 && validTargetHost(host)
}
