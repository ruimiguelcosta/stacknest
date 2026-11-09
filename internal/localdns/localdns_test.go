package localdns

import (
	"testing"

	"github.com/miekg/dns"
)

func query(t *testing.T, addr, name string) *dns.Msg {
	t.Helper()
	message := new(dns.Msg)
	message.SetQuestion(dns.Fqdn(name), dns.TypeA)
	reply, err := dns.Exchange(message, addr)
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

func TestResolvesOnlyTestDomains(t *testing.T) {
	server, err := Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown()
	addr := server.PacketConn.LocalAddr().String()

	reply := query(t, addr, "Shop.EU.test")
	if len(reply.Answer) != 1 || reply.Answer[0].(*dns.A).A.String() != "127.0.0.1" {
		t.Errorf("unexpected answer: %v", reply.Answer)
	}
	if reply := query(t, addr, "example.com"); reply.Rcode != dns.RcodeNameError || len(reply.Answer) != 0 {
		t.Errorf("example.com should not resolve: %v", reply)
	}
}
