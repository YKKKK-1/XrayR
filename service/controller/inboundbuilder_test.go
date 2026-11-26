package controller_test

import (
	"encoding/base64"
	"fmt"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"testing"

	"github.com/XrayR-project/XrayR/api"
	"github.com/XrayR-project/XrayR/common/mylego"
	. "github.com/XrayR-project/XrayR/service/controller"
)

func TestBuildV2ray(t *testing.T) {
	nodeInfo := &api.NodeInfo{
		NodeType:          "V2ray",
		NodeID:            1,
		Port:              1145,
		SpeedLimit:        0,
		AlterID:           2,
		TransportProtocol: "ws",
		Host:              "test.test.tk",
		Path:              "v2ray",
		EnableTLS:         false,
	}
	certConfig := &mylego.CertConfig{
		CertMode:   "http",
		CertDomain: "test.test.tk",
		Provider:   "alidns",
		Email:      "test@gmail.com",
	}
	config := &Config{
		CertConfig: certConfig,
	}
	_, err := InboundBuilder(config, nodeInfo, "test_tag")
	if err != nil {
		t.Error(err)
	}
}

func TestBuildTrojan(t *testing.T) {
	nodeInfo := &api.NodeInfo{
		NodeType:          "Trojan",
		NodeID:            1,
		Port:              1145,
		SpeedLimit:        0,
		AlterID:           2,
		TransportProtocol: "tcp",
		Host:              "trojan.test.tk",
		Path:              "v2ray",
		EnableTLS:         false,
	}
	DNSEnv := make(map[string]string)
	DNSEnv["ALICLOUD_ACCESS_KEY"] = "aaa"
	DNSEnv["ALICLOUD_SECRET_KEY"] = "bbb"
	certConfig := &mylego.CertConfig{
		CertMode:   "dns",
		CertDomain: "trojan.test.tk",
		Provider:   "alidns",
		Email:      "test@gmail.com",
		DNSEnv:     DNSEnv,
	}
	config := &Config{
		CertConfig: certConfig,
	}
	_, err := InboundBuilder(config, nodeInfo, "test_tag")
	if err != nil {
		t.Error(err)
	}
}

func TestBuildSS(t *testing.T) {
	nodeInfo := &api.NodeInfo{
		NodeType:          "Shadowsocks",
		CypherMethod:      "aes-256-gcm",
		NodeID:            1,
		Port:              1145,
		SpeedLimit:        0,
		AlterID:           2,
		TransportProtocol: "tcp",
		Host:              "test.test.tk",
		Path:              "v2ray",
		EnableTLS:         false,
	}
	DNSEnv := make(map[string]string)
	DNSEnv["ALICLOUD_ACCESS_KEY"] = "aaa"
	DNSEnv["ALICLOUD_SECRET_KEY"] = "bbb"
	certConfig := &mylego.CertConfig{
		CertMode:   "dns",
		CertDomain: "trojan.test.tk",
		Provider:   "alidns",
		Email:      "test@me.com",
		DNSEnv:     DNSEnv,
	}
	config := &Config{
		CertConfig: certConfig,
	}
	_, err := InboundBuilder(config, nodeInfo, "test_tag")
	if err != nil {
		t.Error(err)
	}
}

func TestBuildSS2022(t *testing.T) {
	for _, size := range []int{16, 32} {
		method := fmt.Sprintf("2022-blake3-aes-%d-gcm", size*8)
		t.Run(method, func(t *testing.T) {
			node := &api.NodeInfo{NodeType: "Shadowsocks", Port: 1145, TransportProtocol: "tcp", CypherMethod: method, ServerKey: base64.StdEncoding.EncodeToString(make([]byte, size))}
			inbound, err := InboundBuilder(&Config{}, node, "ss")
			if err != nil {
				t.Fatal(err)
			}
			msg, err := inbound.ProxySettings.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			cfg := msg.(*shadowsocks_2022.MultiUserServerConfig)
			if len(cfg.Users) != 1 {
				t.Fatalf("got %d initial users", len(cfg.Users))
			}
			value, err := cfg.Users[0].Account.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			key, err := base64.StdEncoding.DecodeString(value.(*shadowsocks_2022.Account).Key)
			if err != nil || len(key) != size {
				t.Fatalf("initial key length %d, want %d, error %v", len(key), size, err)
			}
		})
	}
}
