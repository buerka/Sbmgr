package mesh

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"strconv"
	"strings"
	"time"
)

// Transport is a traffic policy stored separately from node membership.
type Transport struct {
	Type        string `json:"type"`
	Server      string `json:"server"`
	Port        int    `json:"port"`
	Credential  string `json:"credential,omitempty"`
	Certificate string `json:"certificate,omitempty"`
	ServerKey   string `json:"server_key,omitempty"`
	ClientKey   string `json:"client_key,omitempty"`
}

// Connection contains one side's material, never the remote private key.
type Connection struct {
	Type        string `json:"type"`
	Server      string `json:"server,omitempty"`
	Port        int    `json:"port"`
	Credential  string `json:"credential,omitempty"`
	Certificate string `json:"certificate,omitempty"`
	PrivateKey  string `json:"private_key,omitempty"`
	PeerKey     string `json:"peer_key,omitempty"`
}

func NormalizeTransport(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "s5", "socks5", "socks":
		return "socks"
	case "hy", "hy2", "hysteria2":
		return "hysteria2"
	case "wg", "wireguard":
		return "wireguard"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}
func NewTransport(kind, server string, port int) (Transport, error) {
	t := Transport{Type: NormalizeTransport(kind), Server: server, Port: port}
	switch t.Type {
	case "socks", "hysteria2":
		var secret [32]byte
		if _, err := rand.Read(secret[:]); err != nil {
			return t, errors.New("生成线路凭据失败")
		}
		t.Credential = base64.RawURLEncoding.EncodeToString(secret[:])
		if t.Type == "hysteria2" {
			var err error
			t.Certificate, t.ServerKey, err = newTransportCertificate()
			if err != nil {
				return t, err
			}
		}
	case "wireguard":
		var err error
		t.ServerKey, _, err = KeyPair()
		if err != nil {
			return t, err
		}
		t.ClientKey, _, err = KeyPair()
		if err != nil {
			return t, err
		}
	default:
		return t, errors.New("线路协议支持 SOCKS5、Hysteria2、WireGuard")
	}
	return t, t.Validate()
}
func (t Transport) Connection(incoming bool) *Connection {
	c := &Connection{Type: t.Type, Server: t.Server, Port: t.Port, Credential: t.Credential, Certificate: t.Certificate}
	if incoming {
		c.Server = ""
	}
	switch t.Type {
	case "hysteria2":
		if incoming {
			c.PrivateKey = t.ServerKey
		}
	case "wireguard":
		if incoming {
			c.PrivateKey, c.PeerKey = t.ServerKey, publicKey(t.ClientKey)
		} else {
			c.PrivateKey, c.PeerKey = t.ClientKey, publicKey(t.ServerKey)
		}
	}
	return c
}
func (t Transport) Validate() error {
	if err := t.Connection(true).Validate(true); err != nil {
		return err
	}
	if err := t.Connection(false).Validate(false); err != nil {
		return err
	}
	if (t.Type == "socks" && t.ServerKey != "") || (t.Type != "wireguard" && t.ClientKey != "") {
		return errors.New("线路包含无关协议的密钥")
	}
	return nil
}
func (c Connection) Validate(incoming bool) error {
	if c.Port < 1024 || c.Port > 65535 {
		return errors.New("线路监听端口范围为 1024–65535")
	}
	if incoming {
		if c.Server != "" {
			return errors.New("入站连接不能指定远端主机")
		}
	} else if err := ValidateEndpoint(net.JoinHostPort(c.Server, strconv.Itoa(c.Port))); err != nil {
		return err
	}
	switch c.Type {
	case "socks", "hysteria2":
		if len(c.Credential) < 32 || len(c.Credential) > 128 || strings.IndexFunc(c.Credential, func(r rune) bool { return r <= 32 || r > 126 }) >= 0 || c.PeerKey != "" {
			return errors.New("线路认证参数无效")
		}
		if c.Type == "socks" {
			if c.Certificate != "" || c.PrivateKey != "" {
				return errors.New("SOCKS5 不使用 WG 或 TLS 密钥")
			}
			return nil
		}
		block, rest := pem.Decode([]byte(c.Certificate))
		if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
			return errors.New("HY2 证书无效")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || cert.VerifyHostname("sbmgr-relay") != nil {
			return errors.New("HY2 证书名称无效")
		}
		if incoming {
			if _, err := tls.X509KeyPair([]byte(c.Certificate), []byte(c.PrivateKey)); err != nil {
				return errors.New("HY2 证书与私钥不匹配")
			}
		} else if c.PrivateKey != "" {
			return errors.New("出站不能持有远端 TLS 私钥")
		}
	case "wireguard":
		if publicKey(c.PrivateKey) == "" || !validKey(c.PeerKey) || c.Credential != "" || c.Certificate != "" {
			return errors.New("WG 协议参数无效")
		}
	default:
		return errors.New("线路协议支持 SOCKS5、Hysteria2、WireGuard")
	}
	return nil
}

func (c Connection) checkRuntime() error {
	if c.Type != "hysteria2" {
		return nil
	}
	block, _ := pem.Decode([]byte(c.Certificate))
	if block == nil {
		return errors.New("HY2 证书无效")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return errors.New("HY2 证书无效")
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return errors.New("HY2 线路证书不在有效期内，请轮换线路凭据并重新应用")
	}
	return nil
}
func KeyPair() (private, public string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", errors.New("生成 WG 协议密钥失败")
	}
	return base64.StdEncoding.EncodeToString(k.Bytes()), base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}
func validKey(value string) bool {
	b, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(b) != 32 || base64.StdEncoding.EncodeToString(b) != value {
		return false
	}
	var nonzero byte
	for _, v := range b {
		nonzero |= v
	}
	return nonzero != 0
}
func publicKey(private string) string {
	if !validKey(private) {
		return ""
	}
	b, _ := base64.StdEncoding.DecodeString(private)
	k, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(k.PublicKey().Bytes())
}
func newTransportCertificate() (string, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", errors.New("生成线路证书失败")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", errors.New("生成证书序列失败")
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "sbmgr-relay"}, DNSNames: []string{"sbmgr-relay"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return "", "", errors.New("签发线路证书失败")
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", errors.New("保存线路证书失败")
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})), nil
}
