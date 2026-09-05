//go:build linux

package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecurityReloadHealthFailureRollsBackAppliedConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(name, value string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(value), 0700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("systemctl", `#!/bin/sh
printf '%s\n' "$*" >> "$SBMGR_TEST_CALLS"
case "$1" in
    kill) if [ "$SBMGR_TEST_FAIL_HUP" = 1 ]; then touch "$SBMGR_TEST_FAILED"; fi ;;
    restart) rm -f "$SBMGR_TEST_FAILED" ;;
    is-active) [ ! -f "$SBMGR_TEST_FAILED" ] ;;
    *) exit 1 ;;
esac
`)
	write("nft", "#!/bin/sh\necho 'No such file or directory' >&2\nexit 1\n")
	binary := write("sing-box", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SBMGR_TEST_FAILED", filepath.Join(dir, "failed"))
	t.Setenv("SBMGR_TEST_CALLS", filepath.Join(dir, "calls"))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	base := fmt.Sprintf(`{"inbounds":[{"type":"vless","tag":"in","listen":"127.0.0.1","listen_port":%d,"users":[]}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`, port)
	old := base + "\n"
	s := &State{Service: "sing-box.service", SingBoxBin: binary, InboundTag: "in", BaseConfig: write("base.json", base), ConfigPath: write("runtime.json", old)}
	t.Setenv("SBMGR_TEST_FAIL_HUP", "1")
	if err := applyState(s, false, false, io.Discard); err == nil {
		t.Fatal("successful signal masked inactive runtime")
	}
	stored, err := os.ReadFile(s.ConfigPath)
	if err != nil || string(stored) != old {
		t.Fatal("failed runtime did not restore old configuration")
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"kill -s HUP -- sing-box.service", "is-active --quiet -- sing-box.service", "restart -- sing-box.service"} {
		if !strings.Contains(string(calls), expected) {
			t.Fatal("missing health or rollback operation", expected)
		}
	}
	t.Setenv("SBMGR_TEST_FAIL_HUP", "0")
	if err := applyState(s, false, false, io.Discard); err != nil {
		t.Fatal("healthy reload rejected", err)
	}
	listener.Close()
	if err := reloadAndCheckService(s, false, io.Discard); err == nil {
		t.Fatal("active service without inbound passed health check")
	}
}

func TestSecurityConfigSymlinkAndPrivateParent(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.json")
	alias := filepath.Join(dir, "alias.json")
	if err := os.WriteFile(base, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(base, alias); err != nil {
		t.Fatal(err)
	}
	if validateConfigPaths(base, alias) == nil {
		t.Fatal("symlink alias accepted")
	}
	private := filepath.Join(dir, "new", "nested", "state.txt")
	if err := atomicWrite(private, []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{private, filepath.Dir(private), filepath.Dir(filepath.Dir(private))} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatal("new sensitive path is accessible to non-owner")
		}
	}
}
