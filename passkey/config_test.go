// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package passkey

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/internal/configtext/configtexttest"
)

// nopStore satisfies Store for the config tests, which never call it.
type nopStore struct{ Store }

func validConfig() Config {
	return Config{
		RPID:          "app.example.com",
		RPDisplayName: "Example App",
		Origins:       []string{"https://app.example.com"},
		Store:         nopStore{},
		StartSession: func(context.Context, http.ResponseWriter, *http.Request, account.Identity) error {
			return nil
		},
		CurrentAccount: func(*http.Request) (account.Identity, time.Time, bool, error) {
			return account.Identity{}, time.Time{}, false, nil
		},
		OtherFactors: func(context.Context, account.Identity) (int, error) { return 1, nil },
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
		want   string // empty means valid
	}{
		{"valid", func(*Config) {}, ""},
		{"subdomain origin", func(c *Config) { c.RPID = "example.com" }, ""},
		{"origin with port", func(c *Config) { c.Origins = []string{"https://app.example.com:8443"} }, ""},
		{"several origins", func(c *Config) {
			c.RPID = "example.com"
			c.Origins = []string{"https://example.com", "https://app.example.com"}
		}, ""},
		{"localhost over http", func(c *Config) {
			c.RPID = "localhost"
			c.Origins = []string{"http://localhost:8080"}
		}, ""},
		{"missing rp id", func(c *Config) { c.RPID = "" }, "rp id is required"},
		{"rp id with scheme", func(c *Config) { c.RPID = "https://app.example.com" }, "not a lower case host name"},
		{"rp id with port", func(c *Config) { c.RPID = "app.example.com:443" }, "not a lower case host name"},
		{"rp id upper case", func(c *Config) { c.RPID = "App.Example.com" }, "not a lower case host name"},
		{"rp id with path", func(c *Config) { c.RPID = "app.example.com/x" }, "not a lower case host name"},
		{"rp id trailing dot", func(c *Config) { c.RPID = "app.example.com." }, "not a lower case host name"},
		{"rp id ip", func(c *Config) { c.RPID = "192.0.2.1" }, "IP address"},
		{"rp id single label", func(c *Config) { c.RPID = "intranet" }, "single label"},
		{"empty origins", func(c *Config) { c.Origins = nil }, "origins are required"},
		{"http origin", func(c *Config) { c.Origins = []string{"http://app.example.com"} }, "not https"},
		{"http origin on 127.0.0.1", func(c *Config) {
			c.RPID = "localhost"
			c.Origins = []string{"http://127.0.0.1:8080"}
		}, "not https"},
		{"https localhost under other rp id", func(c *Config) { c.Origins = []string{"https://localhost"} }, "not rp id"},
		{"origin not below rp id", func(c *Config) { c.Origins = []string{"https://other.example.org"} }, "not rp id"},
		{"origin is parent of rp id", func(c *Config) { c.Origins = []string{"https://example.com"} }, "not rp id"},
		{"origin ends like rp id", func(c *Config) { c.Origins = []string{"https://evilapp.example.com"} }, "not rp id"},
		{"one of two origins outside", func(c *Config) {
			c.Origins = []string{"https://app.example.com", "https://app.example.net"}
		}, "not rp id"},
		{"origin with path", func(c *Config) { c.Origins = []string{"https://app.example.com/login"} }, "more than"},
		{"origin with trailing slash", func(c *Config) { c.Origins = []string{"https://app.example.com/"} }, "more than"},
		{"origin with query", func(c *Config) { c.Origins = []string{"https://app.example.com?x=1"} }, "more than"},
		{"origin with empty query", func(c *Config) { c.Origins = []string{"https://app.example.com?"} }, "more than"},
		{"origin with user", func(c *Config) { c.Origins = []string{"https://u@app.example.com"} }, "more than"},
		{"origin upper case", func(c *Config) { c.Origins = []string{"https://App.example.com"} }, "lower case host"},
		{"origin default port", func(c *Config) { c.Origins = []string{"https://app.example.com:443"} }, "default port"},
		{"origin default http port", func(c *Config) {
			c.RPID = "localhost"
			c.Origins = []string{"http://localhost:80"}
		}, "default port"},
		{"origin port out of range", func(c *Config) { c.Origins = []string{"https://app.example.com:99999"} }, "1 to 65535"},
		{"origin port zero", func(c *Config) { c.Origins = []string{"https://app.example.com:0"} }, "1 to 65535"},
		{"origin port leading zero", func(c *Config) { c.Origins = []string{"https://app.example.com:0443"} }, "1 to 65535"},
		{"origin port leading zero only", func(c *Config) { c.Origins = []string{"https://app.example.com:08443"} }, "1 to 65535"},
		{"origin upper case localhost", func(c *Config) {
			c.RPID = "localhost"
			c.Origins = []string{"http://LOCALHOST:5173"}
		}, "lower case host"},
		{"origin colon without port", func(c *Config) { c.Origins = []string{"https://app.example.com:"} }, "colon without a port"},
		{"origin wildcard", func(c *Config) {
			c.RPID = "example.com"
			c.Origins = []string{"https://*.example.com"}
		}, "no wildcard"},
		{"origin upper case scheme", func(c *Config) { c.Origins = []string{"HTTPS://app.example.com"} }, "lower case scheme"},
		{"origin mixed case scheme", func(c *Config) { c.Origins = []string{"Https://app.example.com"} }, "lower case scheme"},
		{"origin empty label", func(c *Config) { c.Origins = []string{"https://app..example.com"} }, "DNS labels"},
		{"origin highest port", func(c *Config) { c.Origins = []string{"https://app.example.com:65535"} }, ""},
		{"app origin", func(c *Config) { c.Origins = []string{"android:apk-key-hash:abc"} }, "not https"},
		{"missing display name", func(c *Config) { c.RPDisplayName = " " }, "rp display name is required"},
		{"display name control", func(c *Config) { c.RPDisplayName = "a\nb" }, "not printable"},
		{"missing store", func(c *Config) { c.Store = nil }, "store is required"},
		{"missing start session", func(c *Config) { c.StartSession = nil }, "start session is required"},
		{"missing current account", func(c *Config) { c.CurrentAccount = nil }, "current account is required"},
		{"missing other factors", func(c *Config) { c.OtherFactors = nil }, "other factors is required"},
		{"challenge ttl too short", func(c *Config) { c.ChallengeTTL = time.Second }, "challenge ttl"},
		{"challenge ttl too long", func(c *Config) { c.ChallengeTTL = time.Hour }, "challenge ttl"},
		{"negative reauth window", func(c *Config) { c.ReauthWindow = -time.Second }, "reauth window"},
		{"negative body limit", func(c *Config) { c.MaxBodyBytes = -1 }, "max body bytes"},
		{"bad trusted origin", func(c *Config) { c.TrustedOrigins = []string{"not an origin"} }, "trusted origin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.change(&cfg)
			h, err := New(cfg)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				if h == nil {
					t.Fatal("New returned nil handlers")
				}
				return
			}
			if err == nil {
				t.Fatalf("New accepted the config, want an error with %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New: got %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// A value that may carry a credential stays out of every startup error, and
// so does the error of url.Parse, which quotes the whole input.
func TestConfigErrorHidesCredentials(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"origin with userinfo", func(c *Config) {
			c.Origins = []string{"https://user:s3cr3tTOKEN@app.example.com"}
		}, "origin"},
		{"origin with a query token", func(c *Config) {
			c.Origins = []string{"https://app.example.com?token=s3cr3tTOKEN"}
		}, "origin"},
		{"origin that is not a URL", func(c *Config) {
			c.Origins = []string{"https://user:s3cr3tTOKEN@app example.com\x7f"}
		}, "is not a URL"},
		{"rp id with userinfo", func(c *Config) { c.RPID = "user:s3cr3tTOKEN@app.example.com" }, "rp id"},
		{"rp id that is a single label", func(c *Config) { c.RPID = "s3cr3tTOKEN@localhost" }, "rp id is a single label"},
		{"rp id that is a pasted token", func(c *Config) { c.RPID = "s3cr3tTOKEN" }, "rp id is a single label"},
		{"origin with an upper case scheme", func(c *Config) {
			c.Origins = []string{"HTTPS://u:s3cr3tTOKEN@app.example.com"}
		}, "origin 1 needs a lower case scheme"},
		{"origin that is not https", func(c *Config) {
			c.Origins = []string{"https://app.example.com", "ftp://u:s3cr3tTOKEN@app.example.com"}
		}, "origin 2 is not https"},
		{"origin on localhost with an upper case host", func(c *Config) {
			c.RPID = "localhost"
			c.Origins = []string{"http://u:s3cr3tTOKEN@LOCALHOST"}
		}, "origin 1 needs a lower case host"},
		{"origin that is a pasted token", func(c *Config) { c.Origins = []string{"s3cr3tTOKEN"} }, "origin 1"},
		{"origin below a refused rp id", func(c *Config) {
			c.RPID = "s3cr3tTOKEN"
			c.Origins = []string{"https://app.example.com"}
		}, "rp id"},
		{"trusted origin with userinfo", func(c *Config) {
			c.TrustedOrigins = []string{"https://user:s3cr3tTOKEN@app.example.com/"}
		}, "trusted origin"},
		{"trusted origin that is not a URL", func(c *Config) {
			c.TrustedOrigins = []string{"https://user:s3cr3tTOKEN@app example.com\x7f"}
		}, "trusted origin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.change(&cfg)
			_, err := New(cfg)
			configtexttest.AssertHidden(t, err, "s3cr3tTOKEN", tt.want)
		})
	}
}

func TestConfigNamesEveryProblem(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Fatal("New accepted an empty config")
	}
	for _, want := range []string{"rp id", "display name", "origins", "store", "start session", "current account", "other factors"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg, err := validConfig().validate()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChallengeTTL != DefaultChallengeTTL || cfg.ReauthWindow != DefaultReauthWindow ||
		cfg.MaxBodyBytes != DefaultMaxBodyBytes || cfg.Now == nil {
		t.Fatalf("defaults not filled: %+v", cfg)
	}
}
