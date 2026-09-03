package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const maxSingBoxCheckDiagnosticBytes = 64 << 10

// cappedDiagnosticBuffer keeps a broken or malicious checker from flooding the
// CUI while still draining its stdout/stderr so the child process can exit.
type cappedDiagnosticBuffer struct {
	buffer    bytes.Buffer
	truncated bool
}

func (buffer *cappedDiagnosticBuffer) Write(data []byte) (int, error) {
	remaining := maxSingBoxCheckDiagnosticBytes - buffer.buffer.Len()
	if remaining > 0 {
		kept := len(data)
		if kept > remaining {
			kept = remaining
		}
		_, _ = buffer.buffer.Write(data[:kept])
	}
	if len(data) > max(remaining, 0) {
		buffer.truncated = true
	}
	return len(data), nil
}

func (buffer *cappedDiagnosticBuffer) writeRedactedTo(writer io.Writer, config []byte) {
	if writer == nil {
		return
	}
	redacted := redactSingBoxDiagnostics(buffer.buffer.Bytes(), config)
	if len(redacted) != 0 {
		_, _ = writer.Write(redacted)
		if redacted[len(redacted)-1] != '\n' {
			_, _ = io.WriteString(writer, "\n")
		}
	}
	if buffer.truncated {
		_, _ = fmt.Fprintln(writer, "sing-box 校验输出过长，已截断")
	}
}

func redactSingBoxDiagnostics(diagnostics, config []byte) []byte {
	if len(diagnostics) == 0 || len(config) == 0 {
		return append([]byte(nil), diagnostics...)
	}
	var root any
	if json.Unmarshal(config, &root) != nil {
		return append([]byte(nil), diagnostics...)
	}
	values := map[string]bool{}
	collectSingBoxSecretValues(root, "", values)
	replacements := make([]string, 0, len(values)*2)
	for value := range values {
		if value == "" {
			continue
		}
		if len(value) < 3 {
			// A raw one- or two-character credential cannot be safely replaced
			// without corrupting virtually every diagnostic word. In that rare
			// case hide the checker detail altogether.
			return []byte("sing-box 校验诊断包含短凭据，详细内容已隐藏\n")
		}
		if quoted, err := json.Marshal(value); err == nil {
			replacements = append(replacements, string(quoted))
		}
		// Raw values are common in protocol diagnostics.
		replacements = append(replacements, value)
	}
	sort.Slice(replacements, func(left, right int) bool {
		return len(replacements[left]) > len(replacements[right])
	})
	text := string(diagnostics)
	for _, value := range replacements {
		text = strings.ReplaceAll(text, value, "<已隐藏>")
	}
	return []byte(text)
}

func collectSingBoxSecretValues(value any, key string, values map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			effectiveKey := childKey
			// Inbound users use {"name","uuid"}; name is the auth_user
			// credential even though the leaf key itself looks generic.
			if strings.EqualFold(key, "users") && strings.EqualFold(childKey, "name") {
				effectiveKey = "auth_user"
			}
			collectSingBoxSecretValues(child, effectiveKey, values)
		}
	case []any:
		for _, child := range typed {
			collectSingBoxSecretValues(child, key, values)
		}
	case string:
		if isSingBoxSecretKey(key) {
			values[typed] = true
		}
	}
}

func isSingBoxSecretKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"), " ", "_"))
	switch normalized {
	case "password", "username", "user", "uuid", "private_key", "pre_shared_key", "preshared_key", "psk", "auth_key", "client_key",
		"token", "access_token", "refresh_token", "secret", "client_secret", "api_key", "apikey",
		"passphrase", "userkey", "authorization", "auth_user", "auth", "auth_str", "cookie", "credential", "credentials",
		"key", "mesh_psk", "mac_key":
		return true
	default:
		return strings.HasSuffix(normalized, "_password") ||
			strings.HasSuffix(normalized, "_secret") ||
			strings.HasSuffix(normalized, "_token") ||
			strings.HasSuffix(normalized, "_private_key") ||
			strings.HasSuffix(normalized, "_auth_key") ||
			strings.HasSuffix(normalized, "_auth") ||
			strings.HasSuffix(normalized, "_psk") ||
			strings.HasSuffix(normalized, "_client_key") ||
			strings.HasSuffix(normalized, "_cookie") ||
			strings.HasSuffix(normalized, "_credential") ||
			strings.HasSuffix(normalized, "_credentials")
	}
}
