package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sbmgr/internal/mesh"
	"strings"
	"testing"
	"time"
)

// Optional local integration: only loopback addresses and temporary credentials
// are used. The caller supplies an independently verified sing-box executable.
func TestMeshSingBoxLoopbackProtocols(t *testing.T) {
	bin := os.Getenv("SBMGR_TEST_SING_BOX")
	if bin == "" {
		t.Skip("set SBMGR_TEST_SING_BOX for local sing-box integration")
	}
	for _, protocols := range []string{"socks", "hysteria2", "wireguard", "socks,wireguard,hysteria2"} {
		t.Run(protocols, func(t *testing.T) {
			kinds := strings.Split(protocols, ",")
			topology := mesh.Topology{ID: "local-test", Master: "master", Revision: 1, Members: []mesh.Member{{ID: "master"}}}
			route := mesh.Route{ID: "test"}
			for i, kind := range kinds {
				id := fmt.Sprintf("node-%d", i)
				topology.Members = append(topology.Members, mesh.Member{ID: id})
				route.Hops = append(route.Hops, id)
				tr, err := mesh.NewTransport(kind, "127.0.0.1", meshTestPort(t))
				if err != nil {
					t.Fatal(err)
				}
				route.Transports = append(route.Transports, tr)
			}
			topology.Routes = []mesh.Route{route}
			clientPort := meshTestPort(t)
			for i := len(topology.Members) - 1; i >= 0; i-- {
				member := topology.Members[i]
				plan, err := topology.Compile(member.ID)
				if err != nil {
					t.Fatal(err)
				}
				cfg := map[string]any{"log": map[string]any{"level": "error"}, "outbounds": []any{map[string]any{"type": "direct", "tag": "base"}}, "route": map[string]any{"final": "base"}}
				if err := plan.Augment(cfg); err != nil {
					t.Fatal(err)
				}
				if i == len(topology.Members)-1 {
					// gVisor correctly rejects tunneled loopback destinations. Route a
					// synthetic destination to the local echo only at the final hop.
					rules := cfg["route"].(map[string]any)["rules"].([]any)
					cfg["route"].(map[string]any)["rules"] = append([]any{map[string]any{"action": "route-options", "override_address": "127.0.0.1"}}, rules...)
				}
				if plan.Master {
					cfg["inbounds"] = []any{map[string]any{"type": "socks", "tag": "client", "listen": "127.0.0.1", "listen_port": clientPort}}
					u := User{Name: "local-test", Enabled: true, Nodes: []Node{{Name: "test", AuthUser: "local-test", Outbound: mesh.RouteTag("test"), RateMark: rateMarkPrefix | 1}}}
					s := &State{Users: []User{u}}
					tags, err := addRateOutbounds(cfg, s, []User{u})
					if err != nil {
						t.Fatal(err)
					}
					cfg["route"].(map[string]any)["final"] = tags["local-test"]
					// Kernel mark enforcement is covered separately on Linux. Removing
					// this dial option permits protocol integration on Windows too.
					for _, item := range cfg["outbounds"].([]any) {
						delete(item.(map[string]any), "routing_mark")
					}
				}
				inbounds, _ := cfg["inbounds"].([]any)
				for _, item := range inbounds {
					item.(map[string]any)["listen"] = "127.0.0.1"
				}
				meshTestRunSingBox(t, bin, cfg)
			}
			address := fmt.Sprintf("127.0.0.1:%d", clientPort)
			deadline := time.Now().Add(5 * time.Second)
			for {
				c, err := net.DialTimeout("tcp4", address, 100*time.Millisecond)
				if err == nil {
					c.Close()
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("local sing-box listener did not start")
				}
				time.Sleep(20 * time.Millisecond)
			}
			meshTestTCP(t, address)
			meshTestUDP(t, address)
		})
	}
}

func meshTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	udp, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	udp.Close()
	return port
}

func meshTestRunSingBox(t *testing.T, bin string, cfg map[string]any) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "candidate.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	check := exec.CommandContext(ctx, bin, "check", "-c", path)
	if err := check.Run(); err != nil {
		t.Fatalf("sing-box rejected local candidate: %v (diagnostics withheld)", err)
	}
	cmd := exec.Command(bin, "run", "-c", path)
	var diagnostic cappedDiagnosticBuffer
	cmd.Stdout, cmd.Stderr = &diagnostic, &diagnostic
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
		if t.Failed() {
			t.Log(string(redactSingBoxDiagnostics(diagnostic.buffer.Bytes(), raw)))
		}
	})
}

func meshTestSOCKS(t *testing.T, address string, command byte, port int) (net.Conn, []byte) {
	t.Helper()
	c, err := net.DialTimeout("tcp4", address, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	var greeting [2]byte
	if _, err := io.ReadFull(c, greeting[:]); err != nil || greeting != [2]byte{5, 0} {
		t.Fatal("SOCKS negotiation failed")
	}
	request := []byte{5, command, 0, 1, 198, 18, 0, 1, byte(port >> 8), byte(port)}
	if _, err := c.Write(request); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(c, header); err != nil || header[1] != 0 {
		t.Fatal("SOCKS connection failed")
	}
	length := 4
	if header[3] == 4 {
		length = 16
	} else if header[3] != 1 {
		t.Fatal("unexpected SOCKS bind address")
	}
	bound := make([]byte, length+2)
	if _, err := io.ReadFull(c, bound); err != nil {
		t.Fatal(err)
	}
	return c, bound
}

func meshTestTCP(t *testing.T, address string) {
	t.Helper()
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		c, err := echo.Accept()
		if err == nil {
			defer c.Close()
			c.SetDeadline(time.Now().Add(8 * time.Second))
			_, _ = io.Copy(c, c)
		}
	}()
	c, _ := meshTestSOCKS(t, address, 1, echo.Addr().(*net.TCPAddr).Port)
	payload := []byte("local TCP relay")
	if _, err := c.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(c, got); err != nil || string(got) != string(payload) {
		t.Fatal("TCP relay did not reach local echo")
	}
}

func meshTestUDP(t *testing.T, address string) {
	t.Helper()
	echo, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		var buf [1024]byte
		n, peer, err := echo.ReadFrom(buf[:])
		if err == nil {
			_, _ = echo.WriteTo(buf[:n], peer)
		}
	}()
	_, bound := meshTestSOCKS(t, address, 3, 0)
	port := binary.BigEndian.Uint16(bound[len(bound)-2:])
	c, err := net.DialTimeout("udp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(8 * time.Second))
	echoPort := echo.LocalAddr().(*net.UDPAddr).Port
	payload := "local UDP relay"
	datagram := append([]byte{0, 0, 0, 1, 198, 18, 0, 1, byte(echoPort >> 8), byte(echoPort)}, []byte(payload)...)
	if _, err := c.Write(datagram); err != nil {
		t.Fatal(err)
	}
	var response [1024]byte
	n, err := c.Read(response[:])
	if err != nil || n < 10 || string(response[10:n]) != payload {
		t.Fatal("UDP relay did not reach local echo")
	}
}
