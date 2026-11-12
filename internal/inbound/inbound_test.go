package inbound

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestFormats(t *testing.T) {
	var got map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = map[string]string{"type": r.Header.Get("Content-Type"), "body": string(body)}
	}))
	defer server.Close()
	message := Message{From: "ana@example.test", To: "suporte@app.test", Subject: "Olá", Text: "Preciso de ajuda", HTML: "<p>Preciso de ajuda</p>"}

	send := func(format string) {
		t.Helper()
		req, err := Request(context.Background(), server.URL, format, message)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := server.Client().Do(req); err != nil {
			t.Fatal(err)
		}
	}

	send("raw")
	if got["type"] != "message/rfc822" || !strings.Contains(got["body"], "Subject: Olá") || !strings.Contains(got["body"], "multipart/alternative") {
		t.Errorf("raw: %+v", got)
	}
	send("postmark")
	var postmark map[string]any
	if err := json.Unmarshal([]byte(got["body"]), &postmark); err != nil || postmark["TextBody"] != "Preciso de ajuda" || postmark["From"] != "ana@example.test" {
		t.Errorf("postmark: %v %+v", err, postmark)
	}
	send("mailgun")
	if got["type"] != "application/x-www-form-urlencoded" || !strings.Contains(got["body"], "body-plain=Preciso+de+ajuda") {
		t.Errorf("mailgun: %+v", got)
	}
	if _, err := Request(context.Background(), server.URL, "fax", message); err == nil {
		t.Error("expected an error for an unknown format")
	}
}
