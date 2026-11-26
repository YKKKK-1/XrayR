package panel

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/XrayR-project/XrayR/api"
	"github.com/XrayR-project/XrayR/app/mydispatcher"
	"github.com/XrayR-project/XrayR/common/limiter"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/features/stats"
)

const visionID = "ab0c5671-2018-407d-b399-ac2ed06b9ffd"
const visionEmail = "vision|vision-test@example.com|1"

type visionEndpoint struct {
	client  *http.Client
	url     string
	stats   stats.Manager
	limiter *limiter.Limiter
}

func startVisionEndpoint(t testing.TB, security string) visionEndpoint {
	t.Helper()
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the entire upload before echoing, including for HTTP/1.1.
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write(payload)
	}))
	origin.EnableHTTP2 = true
	origin.StartTLS()
	t.Cleanup(origin.Close)
	cert := origin.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	certFile := file("cert.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}))
	keyFile := file("key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	serverStream := fmt.Sprintf(`{"network":"tcp","security":"tls","tlsSettings":{
		"certificates":[{"certificateFile":%q,"keyFile":%q}]}}`, certFile, keyFile)
	clientStream := fmt.Sprintf(`{"network":"tcp","security":"tls","tlsSettings":{
		"serverName":"example.com","disableSystemRoot":true,"certificates":[{"certificateFile":%q,"usage":"verify"}]}}`, certFile)
	if security == "reality" {
		key, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		serverStream = fmt.Sprintf(`{"network":"tcp","security":"reality","realitySettings":{
			"target":%q,"serverNames":["example.com"],"privateKey":%q,"shortIds":["01"]}}`,
			origin.Listener.Addr().String(), base64.RawURLEncoding.EncodeToString(key.Bytes()))
		clientStream = fmt.Sprintf(`{"network":"tcp","security":"reality","realitySettings":{
			"serverName":"example.com","fingerprint":"chrome","publicKey":%q,"shortId":"01"}}`,
			base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()))
	}

	inbound := fmt.Sprintf(`[{
		"listen":"127.0.0.1","port":%d,"protocol":"vless","tag":"vision",
		"settings":{"clients":[{"id":%q,"flow":"xtls-rprx-vision","email":%q}],"decryption":"none"},
		"streamSettings":%s,"sniffing":{"enabled":true,"destOverride":["http","tls"],"routeOnly":true}
	}]`, port, visionID, visionEmail, serverStream)
	direct := fmt.Sprintf(`[{"protocol":"freedom","tag":"vision","settings":{
		"finalRules":[{"action":"allow","ip":["127.0.0.1"],"port":%d}]}}]`, origin.Listener.Addr().(*net.TCPAddr).Port)
	server := New(&Config{
		LogConfig:          &LogConfig{Level: "none"},
		InboundConfigPath:  file("in.json", []byte(inbound)),
		OutboundConfigPath: file("out.json", []byte(direct)),
	})
	server.Start()
	t.Cleanup(server.Close)
	outbound := fmt.Sprintf(`{"log":{"loglevel":"none"},"outbounds":[{
		"protocol":"vless","settings":{"vnext":[{"address":"127.0.0.1","port":%d,
		"users":[{"id":%q,"encryption":"none","flow":"xtls-rprx-vision"}]}]},"streamSettings":%s
	}]}`, port, visionID, clientStream)
	xc, err := core.StartInstance("json", []byte(outbound))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { xc.Close() })
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	dialCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		p, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			return nil, err
		}
		return core.Dial(dialCtx, xc, xnet.TCPDestination(xnet.ParseAddress(host), xnet.Port(p)))
	}}
	t.Cleanup(tr.CloseIdleConnections)
	return visionEndpoint{&http.Client{Transport: tr, Timeout: 15 * time.Second}, origin.URL, server.Server.GetFeature(stats.ManagerType()).(stats.Manager), server.Server.GetFeature(routing.DispatcherType()).(*mydispatcher.DefaultDispatcher).Limiter}
}

func (e visionEndpoint) transfer(t testing.TB, payload []byte, digest [32]byte, closeConn bool) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.url, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Close = closeConn
	res, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	sum := sha256.New()
	n, err := io.Copy(sum, res.Body)
	if err != nil || res.StatusCode != http.StatusOK || n != int64(len(payload)) || !bytes.Equal(sum.Sum(nil), digest[:]) {
		t.Fatalf("echo returned status %d, %d bytes, SHA-256 %x, error %v", res.StatusCode, n, sum.Sum(nil), err)
	}
}

func TestVisionRoundTrip(t *testing.T) {
	for _, security := range []string{"tls", "reality"} {
		t.Run(security, func(t *testing.T) {
			e := startVisionEndpoint(t, security)
			payload := bytes.Repeat([]byte("vision echo test"), 1<<16)
			digest := sha256.Sum256(payload)
			for i := 0; i < 3; i++ {
				e.transfer(t, payload, digest, true)
			}
			for _, direction := range []string{"uplink", "downlink"} {
				counter := e.stats.GetCounter("user>>>" + visionEmail + ">>>traffic>>>" + direction)
				if counter == nil || counter.Value() < int64(3*len(payload)) {
					t.Fatalf("missing %s traffic", direction)
				}
			}
		})
	}
}

func TestVisionLimits(t *testing.T) {
	e := startVisionEndpoint(t, "tls")
	const speed = 1 << 20 // bytes per second, shared between both directions
	users := []api.UserInfo{{UID: 1, Email: "vision-test@example.com", SpeedLimit: speed, DeviceLimit: 1}}
	if err := e.limiter.AddInboundLimiter("vision", 0, &users, nil); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, speed)
	start := time.Now()
	e.transfer(t, payload, sha256.Sum256(payload), true)
	// The bucket starts with one second of credit. Two MiB need at least one more second.
	if elapsed := time.Since(start); elapsed < 950*time.Millisecond {
		t.Fatalf("transfer exceeded configured limit: %v", elapsed)
	}
	_, _, rejected := e.limiter.GetUserBucket("vision", visionEmail, "127.0.0.2")
	if !rejected {
		t.Fatal("second source was allowed after a Vision transfer")
	}
	online, err := e.limiter.GetOnlineDevice("vision")
	if err != nil || len(*online) != 1 || (*online)[0].UID != 1 || (*online)[0].IP != "127.0.0.1" {
		t.Fatalf("unexpected online devices: %v, %v", online, err)
	}
}

func BenchmarkVisionRoundTrip(b *testing.B) {
	e := startVisionEndpoint(b, "tls")
	payload := bytes.Repeat([]byte("vision echo test"), 1<<16)
	digest := sha256.Sum256(payload)
	e.transfer(b, payload, digest, false)
	b.SetBytes(2 * int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.transfer(b, payload, digest, false)
	}
	b.StopTimer()
}
