package mydispatcher

import (
	"context"
	"testing"

	"github.com/XrayR-project/XrayR/api"
	"github.com/XrayR-project/XrayR/common/limiter"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
)

func TestOnlineIPv6(t *testing.T) {
	const ip = "2001:db8::1"
	d := DefaultDispatcher{Limiter: limiter.New()}
	users := []api.UserInfo{{UID: 1, Email: "test"}}
	if err := d.Limiter.AddInboundLimiter("node", 0, &users, nil); err != nil {
		t.Fatal(err)
	}
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{Tag: "node", User: &protocol.MemoryUser{Email: "node|test|1"}, Source: net.TCPDestination(net.ParseAddress(ip), 1234)})
	if _, err := d.userBucket(ctx); err != nil {
		t.Fatal(err)
	}
	online, err := d.Limiter.GetOnlineDevice("node")
	if err != nil || len(*online) != 1 || (*online)[0].IP != ip {
		t.Fatalf("unexpected online report: %v, %v", online, err)
	}
}
