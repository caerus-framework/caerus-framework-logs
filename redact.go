package cf_logs

import (
	"log/slog"
	"net"
	"net/url"
	"strings"
)

// RedactedPlaceholder is what cooperative secret helpers print instead of a
// credential. Configuration’s `secret:"redact"` tag and RedactedString both
// resolve to this string. It is not a fingerprint or a hash.
const RedactedPlaceholder = "[redacted]"

// RedactedString is a secret that may be passed to slog. It implements
// slog.LogValuer so the cleartext never appears in a record: empty stays
// empty; any other value becomes [redacted].
//
// This is cooperative. fmt.Sprintf, error strings, and slog.Any on a raw
// struct still leak. Mark the field, wrap the value, or call configuration’s
// LogArgs — do not expect a process-wide ReplaceAttr to catch everything.
type RedactedString string

// LogValue implements slog.LogValuer.
func (s RedactedString) LogValue() slog.Value {
	if s == "" {
		return slog.StringValue("")
	}
	return slog.StringValue(RedactedPlaceholder)
}

// SecretSet is a presence-only attr: key_set=true when v is non-empty, without
// printing v. Use on reload summaries (“a password exists”) instead of logging
// the password field.
func SecretSet(key, v string) slog.Attr {
	return slog.Bool(key+"_set", v != "")
}

// ReplaceAttrSecretKeys returns a slog HandlerOptions.ReplaceAttr function
// that rewrites matching attribute keys to [redacted] when the value is a
// non-empty string. Opt in on a handler you own; it does not wrap slog.Default
// and does not walk structs. Prefer RedactedString / secret tags.
func ReplaceAttrSecretKeys(keys ...string) func(groups []string, a slog.Attr) slog.Attr {
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		set[k] = struct{}{}
	}
	return func(_ []string, a slog.Attr) slog.Attr {
		if _, ok := set[a.Key]; !ok {
			return a
		}
		if a.Value.Kind() == slog.KindString && a.Value.String() != "" {
			a.Value = slog.StringValue(RedactedPlaceholder)
		}
		return a
	}
}

// RedactURLUserinfo returns a URL string with the password (and only the
// password) in userinfo replaced by [redacted]. Username stays. If raw is not
// a URL or has no userinfo, it is returned unchanged. If parsing fails on a
// string that looks like a URL with userinfo, the function returns a generic
// placeholder so a bad DSN cannot leak through %w.
func RedactURLUserinfo(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		if strings.Contains(raw, "://") && strings.Contains(raw, "@") {
			return "[unparseable-url]"
		}
		return raw
	}
	if u.User == nil {
		return raw
	}
	user := u.User.Username()
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(user, RedactedPlaceholder)
	}
	return u.String()
}

// IPMode selects how ClientIP formats an address for logs.
type IPMode string

const (
	// IPFull logs the address as given (after stripping a :port if present).
	IPFull IPMode = "full"
	// IPPartial keeps IPv4 /24 (a.b.c.0) and IPv6 /48. Hostnames are omitted.
	IPPartial IPMode = "partial"
	// IPOmit logs nothing (empty string).
	IPOmit IPMode = "omit"
)

// ParseIPMode maps full|partial|omit (case-insensitive). Unknown names error.
func ParseIPMode(name string) (IPMode, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "full":
		return IPFull, nil
	case "partial":
		return IPPartial, nil
	case "omit":
		return IPOmit, nil
	default:
		return IPOmit, errUnknownIPMode(name)
	}
}

type ipModeError string

func errUnknownIPMode(name string) error {
	return ipModeError(name)
}

func (e ipModeError) Error() string {
	return "cf_logs: unknown IP log mode " + string(e) + " (want full, partial, or omit)"
}

// ClientIP formats an already-chosen client identity for a log record.
// Pass the address the app trusts (for example r.RemoteAddr after your own
// proxy policy). Do not pass X-Forwarded-For here: this helper does not
// decide whether a header is forged.
func ClientIP(addr string, mode IPMode) string {
	host := addrHost(addr)
	switch mode {
	case IPFull:
		return host
	case IPOmit:
		return ""
	case IPPartial:
		return partialIP(host)
	default:
		return ""
	}
}

func addrHost(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return host
	}
	return addr
}

func partialIP(host string) string {
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		v4[3] = 0
		return v4.String()
	}
	mask := net.CIDRMask(48, 128)
	return ip.Mask(mask).String()
}
