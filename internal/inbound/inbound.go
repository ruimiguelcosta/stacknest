// Package inbound simulates an email arriving at a project, the way services
// such as Postmark or Mailgun deliver inbound mail to a webhook.
package inbound

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Message struct {
	From, To, Subject, Text, HTML string
}

// Formats lists the payloads Post can send.
var Formats = []string{"raw", "postmark", "mailgun"}

// MIME renders the message as an RFC 5322 email.
func (m Message) MIME(id string, date time.Time) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s>\r\nMIME-Version: 1.0\r\n",
		m.From, m.To, m.Subject, date.Format(time.RFC1123Z), id)
	if m.HTML == "" {
		fmt.Fprintf(&b, "Content-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", m.Text)
		return b.Bytes()
	}
	boundary := "stacknest-" + strings.ReplaceAll(id, "@", "-")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", boundary, m.Text)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s\r\n", boundary, m.HTML)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

// Request builds the HTTP request for a format.
func Request(ctx context.Context, target, format string, m Message) (*http.Request, error) {
	now := time.Now()
	id := fmt.Sprintf("%d@stacknest.test", now.UnixNano())
	var body io.Reader
	contentType := ""
	switch format {
	case "raw":
		body, contentType = bytes.NewReader(m.MIME(id, now)), "message/rfc822"
	case "postmark":
		payload, _ := json.Marshal(map[string]any{
			"From": m.From, "FromFull": map[string]string{"Email": m.From},
			"To": m.To, "ToFull": []map[string]string{{"Email": m.To}},
			"Subject": m.Subject, "TextBody": m.Text, "HtmlBody": m.HTML,
			"StrippedTextReply": m.Text, "MessageID": id, "Date": now.Format(time.RFC1123Z),
			"Headers":     []map[string]string{{"Name": "Message-ID", "Value": "<" + id + ">"}},
			"Attachments": []any{},
		})
		body, contentType = bytes.NewReader(payload), "application/json"
	case "mailgun":
		form := url.Values{
			"sender": {m.From}, "from": {m.From}, "recipient": {m.To}, "To": {m.To},
			"subject": {m.Subject}, "body-plain": {m.Text}, "stripped-text": {m.Text},
			"body-html": {m.HTML}, "Message-Id": {"<" + id + ">"}, "timestamp": {fmt.Sprint(now.Unix())},
			"body-mime": {string(m.MIME(id, now))},
		}
		body, contentType = strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"
	default:
		return nil, fmt.Errorf("unknown format %q (use %s)", format, strings.Join(Formats, ", "))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "stacknest-inbound")
	return req, nil
}

// Client reaches .test sites even before the system resolver and certificate
// are set up: .test names go to this Mac and StackNest's CA is trusted.
func Client(rootCA string) *http.Client {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if pem, err := os.ReadFile(rootCA); err == nil {
		pool.AppendCertsFromPEM(pem)
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err == nil && (host == "test" || strings.HasSuffix(host, ".test")) {
				address = net.JoinHostPort("127.0.0.1", port)
			}
			return dialer.DialContext(ctx, network, address)
		},
	}
	return &http.Client{Transport: transport, Timeout: 60 * time.Second}
}
