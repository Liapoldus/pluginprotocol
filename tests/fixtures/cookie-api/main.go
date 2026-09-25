package main

import (
	"encoding/json"
	"fmt"
	"os"

	protocol "github.com/Liapoldus/pluginprotocol"
)

type result struct {
	Error    string   `json:"error,omitempty"`
	Headers  []string `json:"headers,omitempty"`
	Pairs    []string `json:"pairs,omitempty"`
	Redacted bool     `json:"redacted,omitempty"`
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	var r result
	switch os.Args[1] {
	case "typed-actions":
		_, headers, err := protocol.DecodeHTTPResponseAction([]byte(`{"status":302,"cookies":[{"name":"theme","value":"light","secure":true,"httpOnly":false,"sameSite":"Lax"},{"name":"session","value":"secret-value","path":"/","secure":true,"httpOnly":true,"sameSite":"Lax"}]}`), "www.example.com")
		if err != nil {
			r.Error = err.Error()
		} else {
			r.Headers = headers
		}
	case "unknown-field":
		_, headers, err := protocol.DecodeHTTPResponseAction([]byte(`{"status":200,"cookies":[{"name":"session","value":"secret-value","secure":true,"httpOnly":true,"unknown":"x"}]}`), "example.com")
		if err != nil {
			r.Error = err.Error()
		}
		r.Headers = headers
		r.Redacted = err == nil || !contains(r.Error, "secret-value")
	case "same-site-none":
		_, headers, err := protocol.DecodeHTTPResponseAction([]byte(`{"status":200,"cookies":[{"name":"session","value":"secret-value","secure":false,"httpOnly":true,"sameSite":"None"}]}`), "example.com")
		if err != nil {
			r.Error = err.Error()
		}
		r.Headers = headers
		r.Redacted = err == nil || !contains(r.Error, "secret-value")
	case "host-prefix":
		_, headers, err := protocol.DecodeHTTPResponseAction([]byte(`{"status":200,"cookies":[{"name":"__Host-session","value":"secret-value","secure":true,"httpOnly":true}]}`), "example.com")
		if err != nil {
			r.Error = err.Error()
		}
		r.Headers = headers
	case "secure-prefix":
		_, headers, err := protocol.DecodeHTTPResponseAction([]byte(`{"status":200,"cookies":[{"name":"__Secure-session","value":"secret-value","secure":false,"httpOnly":true}]}`), "example.com")
		if err != nil {
			r.Error = err.Error()
		}
		r.Headers = headers
	case "domain-valid":
		_, headers, err := protocol.DecodeHTTPResponseAction([]byte(`{"status":200,"cookies":[{"name":"session","value":"x","domain":"example.com","secure":true,"httpOnly":true}]}`), "www.example.com:8443")
		if err != nil {
			r.Error = err.Error()
		} else {
			r.Headers = headers
		}
	case "domain-invalid":
		_, headers, err := protocol.DecodeHTTPResponseAction([]byte(`{"status":200,"cookies":[{"name":"session","value":"secret-value","domain":"attacker.test","secure":true,"httpOnly":true}]}`), "www.example.com")
		if err != nil {
			r.Error = err.Error()
		}
		r.Headers = headers
		r.Redacted = err == nil || !contains(r.Error, "secret-value")
	case "public-suffix":
		_, headers, err := protocol.DecodeHTTPResponseAction([]byte(`{"status":200,"cookies":[{"name":"session","value":"secret-value","domain":"co.uk","secure":true,"httpOnly":true}]}`), "www.example.co.uk")
		if err != nil {
			r.Error = err.Error()
		}
		r.Headers = headers
		r.Redacted = err == nil || !contains(r.Error, "secret-value")
	case "atomic":
		_, headers, err := protocol.DecodeHTTPResponseAction([]byte(`{"status":200,"cookies":[{"name":"ok","value":"x","secure":true,"httpOnly":false},{"name":"__Host-invalid","value":"x","secure":true,"httpOnly":false,"domain":"example.com"}]}`), "www.example.com")
		if err != nil {
			r.Error = err.Error()
		}
		r.Headers = headers
	case "policy-filter":
		policy, err := protocol.DecodeCookiePolicy([]byte(`{"version":1,"instanceId":"identity","capability":"identity.callback","allowedNames":["session"]}`))
		if err == nil {
			var filtered []protocol.CookiePair
			filtered, err = protocol.FilterCookiePairs(policy, "identity", "identity.callback", []protocol.CookiePair{{Name: "other", Value: "omit"}, {Name: "session", Value: "first"}, {Name: "session", Value: "second"}})
			if err == nil {
				for _, pair := range filtered {
					r.Pairs = append(r.Pairs, pair.Name+"="+pair.Value)
				}
			}
		}
		if err != nil {
			r.Error = err.Error()
		}
	case "policy-scope":
		policy, err := protocol.DecodeCookiePolicy([]byte(`{"version":1,"instanceId":"identity","capability":"identity.callback","allowedNames":["session"]}`))
		if err == nil {
			_, err = protocol.FilterCookiePairs(policy, "other", "identity.callback", []protocol.CookiePair{{Name: "session", Value: "secret-value"}})
		}
		if err != nil {
			r.Error = err.Error()
		}
		r.Redacted = err == nil || !contains(r.Error, "secret-value")
	case "policy-invalid":
		_, err := protocol.DecodeCookiePolicy([]byte(`{"version":1,"instanceId":"identity","capability":"identity.callback","allowedNames":["*"]}`))
		if err != nil {
			r.Error = err.Error()
		}
	case "request-invalid":
		policy, err := protocol.DecodeCookiePolicy([]byte(`{"version":1,"instanceId":"identity","capability":"identity.callback","allowedNames":["session"]}`))
		if err == nil {
			_, err = protocol.FilterCookiePairs(policy, "identity", "identity.callback", []protocol.CookiePair{{Name: "session", Value: "secret\rvalue"}})
		}
		if err != nil {
			r.Error = err.Error()
		}
		r.Redacted = err == nil || !contains(r.Error, "secret")
	default:
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, "fixture output failed")
		os.Exit(1)
	}
}

func contains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
