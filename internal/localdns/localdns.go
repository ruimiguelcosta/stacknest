// Package localdns answers every *.test name with the loopback address.
package localdns

import (
	"net"
	"strings"

	"github.com/miekg/dns"
)

func handle(writer dns.ResponseWriter, request *dns.Msg) {
	reply := new(dns.Msg)
	reply.SetReply(request)
	reply.Authoritative = true
	for _, question := range request.Question {
		name := strings.ToLower(question.Name)
		if name != "test." && !strings.HasSuffix(name, ".test.") {
			reply.Rcode = dns.RcodeNameError
			continue
		}
		if question.Qtype == dns.TypeA {
			reply.Answer = append(reply.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.IPv4(127, 0, 0, 1),
			})
		}
	}
	_ = writer.WriteMsg(reply)
}

// Start binds addr synchronously, so port conflicts surface as an error.
func Start(addr string) (*dns.Server, error) {
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	server := &dns.Server{PacketConn: conn, Handler: dns.HandlerFunc(handle)}
	go func() { _ = server.ActivateAndServe() }()
	return server, nil
}
