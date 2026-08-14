package cf_logs

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactedStringNeverCleartext(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, nil))
	secret := "s3cret-value"
	l.Info("reload", "password", RedactedString(secret), "host", "db.example")
	out := buf.String()
	if strings.Contains(out, secret) {
		t.Fatalf("cleartext leaked: %s", out)
	}
	if !strings.Contains(out, RedactedPlaceholder) {
		t.Fatalf("want %s in %s", RedactedPlaceholder, out)
	}
	if !strings.Contains(out, "host=db.example") {
		t.Fatalf("unmarked string should stay visible: %s", out)
	}
}

func TestRedactedStringEmptyStaysEmpty(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, nil))
	l.Info("reload", "password", RedactedString(""))
	out := buf.String()
	if strings.Contains(out, RedactedPlaceholder) {
		t.Fatalf("empty secret should not print placeholder: %s", out)
	}
}

func TestSecretSetPresenceOnly(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, nil))
	l.Info("reload", SecretSet("password", "s3cret-value"))
	out := buf.String()
	if strings.Contains(out, "s3cret-value") {
		t.Fatalf("presence leaked value: %s", out)
	}
	if !strings.Contains(out, "password_set=true") {
		t.Fatalf("want password_set=true in %s", out)
	}
}

func TestReplaceAttrSecretKeys(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: ReplaceAttrSecretKeys("password", "api_key"),
	})
	l := slog.New(h)
	l.Info("x", "password", "s3cret-value", "api_key", "re_live", "host", "db")
	out := buf.String()
	if strings.Contains(out, "s3cret-value") || strings.Contains(out, "re_live") {
		t.Fatalf("cleartext leaked: %s", out)
	}
	if !strings.Contains(out, "host=db") {
		t.Fatalf("unmarked key should remain: %s", out)
	}
}

func TestRedactURLUserinfo(t *testing.T) {
	in := "postgres://alice:s3cret-value@db.example:5432/app"
	got := RedactURLUserinfo(in)
	if strings.Contains(got, "s3cret-value") {
		t.Fatalf("password leaked: %s", got)
	}
	if !strings.Contains(got, "alice") || !strings.Contains(got, "db.example") {
		t.Fatalf("username/host should remain: %s", got)
	}
	if !strings.Contains(got, "redacted") {
		t.Fatalf("want redacted userinfo: %s", got)
	}
}

func TestClientIPModes(t *testing.T) {
	if got := ClientIP("10.1.2.3:443", IPFull); got != "10.1.2.3" {
		t.Fatalf("full = %q", got)
	}
	if got := ClientIP("10.1.2.3:443", IPPartial); got != "10.1.2.0" {
		t.Fatalf("partial v4 = %q", got)
	}
	if got := ClientIP("10.1.2.3:443", IPOmit); got != "" {
		t.Fatalf("omit = %q", got)
	}
	if got := ClientIP("evil.example", IPPartial); got != "" {
		t.Fatalf("hostname partial should omit, got %q", got)
	}
}

func TestParseIPMode(t *testing.T) {
	m, err := ParseIPMode("PARTIAL")
	if err != nil || m != IPPartial {
		t.Fatalf("got %q %v", m, err)
	}
	if _, err := ParseIPMode("hash"); err == nil {
		t.Fatal("want error")
	}
}
