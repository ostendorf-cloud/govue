// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"strings"
	"testing"
	"time"
)

const (
	testID  = "0a1b2c3d-0000-4000-8000-000000000001"
	testURL = "https://umami.example.test"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		enabled bool
		errs    []string
	}{
		{name: "both unset is off", env: nil, want: Config{Timeout: DefaultTimeout, QueueSize: DefaultQueueSize}},
		{
			name:    "both set",
			env:     map[string]string{EnvURL: testURL + "/", EnvWebsiteID: testID},
			want:    Config{URL: testURL, WebsiteID: testID, Timeout: DefaultTimeout, QueueSize: DefaultQueueSize},
			enabled: true,
		},
		{
			name:    "timeout and queue size",
			env:     map[string]string{EnvURL: testURL, EnvWebsiteID: testID, EnvTimeout: "2s", EnvQueueSize: "8"},
			want:    Config{URL: testURL, WebsiteID: testID, Timeout: 2 * time.Second, QueueSize: 8},
			enabled: true,
		},
		{name: "url without id", env: map[string]string{EnvURL: testURL}, errs: []string{"must be set together"}},
		{name: "id without url", env: map[string]string{EnvWebsiteID: testID}, errs: []string{"must be set together"}},
		{
			name: "bad id",
			env:  map[string]string{EnvURL: testURL, EnvWebsiteID: "not-a-uuid"},
			errs: []string{EnvWebsiteID + " must be the lowercase UUID"},
		},
		{
			name: "uppercase id",
			env:  map[string]string{EnvURL: testURL, EnvWebsiteID: strings.ToUpper(testID)},
			errs: []string{EnvWebsiteID + " must be the lowercase UUID"},
		},
		{
			name: "bad url",
			env:  map[string]string{EnvURL: "ftp://umami.example.test", EnvWebsiteID: testID},
			errs: []string{EnvURL + " must start with https://"},
		},
		{
			name: "userinfo",
			env:  map[string]string{EnvURL: "https://user:pw-secret@umami.example.test", EnvWebsiteID: testID},
			errs: []string{EnvURL + " must not contain credentials"},
		},
		{
			name: "query",
			env:  map[string]string{EnvURL: testURL + "/?key=secret", EnvWebsiteID: testID},
			errs: []string{EnvURL + " must not contain a query"},
		},
		{
			name: "fragment",
			env:  map[string]string{EnvURL: testURL + "/#x", EnvWebsiteID: testID},
			errs: []string{EnvURL + " must not contain a query"},
		},
		{
			name: "plain http on a public host",
			env:  map[string]string{EnvURL: "http://umami.example.test", EnvWebsiteID: testID},
			errs: []string{EnvURL + " must use https"},
		},
		{name: "plain http on localhost", env: map[string]string{EnvURL: "http://localhost:3000", EnvWebsiteID: testID}, enabled: true},
		{name: "plain http on loopback", env: map[string]string{EnvURL: "http://127.0.0.1:3000", EnvWebsiteID: testID}, enabled: true},
		{name: "plain http on ipv6 loopback", env: map[string]string{EnvURL: "http://[::1]:3000", EnvWebsiteID: testID}, enabled: true},
		{name: "plain http on a service name", env: map[string]string{EnvURL: "http://umami:3000", EnvWebsiteID: testID}, enabled: true},
		{name: "plain http on a svc name", env: map[string]string{EnvURL: "http://umami.stats.svc:3000", EnvWebsiteID: testID}, enabled: true},
		{
			name: "plain http on a cluster domain",
			env:  map[string]string{EnvURL: "http://umami.stats.svc.cluster.local", EnvWebsiteID: testID}, enabled: true,
		},
		{
			name: "plain http on a private address is refused",
			env:  map[string]string{EnvURL: "http://10.0.0.5:3000", EnvWebsiteID: testID},
			errs: []string{EnvURL + " must use https"},
		},
		{name: "bad timeout", env: map[string]string{EnvTimeout: "soon"}, errs: []string{EnvTimeout}},
		{name: "timeout out of range", env: map[string]string{EnvTimeout: "1h"}, errs: []string{EnvTimeout}},
		{name: "queue size zero", env: map[string]string{EnvQueueSize: "0"}, errs: []string{EnvQueueSize}},
		{name: "queue size not a number", env: map[string]string{EnvQueueSize: "many"}, errs: []string{EnvQueueSize}},
		{
			name: "every problem at once",
			env: map[string]string{
				EnvURL: "http://user:pw@umami.example.test/?k=v", EnvWebsiteID: "x", EnvTimeout: "never", EnvQueueSize: "-1",
			},
			errs: []string{EnvURL, EnvWebsiteID, EnvTimeout, EnvQueueSize},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadConfig(env(tt.env))
			if len(tt.errs) > 0 {
				if err == nil {
					t.Fatalf("LoadConfig returned no error, config %+v", got)
				}
				for _, want := range tt.errs {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not contain %q", err, want)
					}
				}
				for _, secret := range []string{"pw-secret", "user:pw", "key=secret", "?k=v", "umami.example.test"} {
					if strings.Contains(err.Error(), secret) {
						t.Errorf("error %q repeats the configured URL part %q", err, secret)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if got.Enabled() != tt.enabled {
				t.Errorf("Enabled = %v, want %v", got.Enabled(), tt.enabled)
			}
			if tt.want != (Config{}) && got != tt.want {
				t.Errorf("config = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadConfigNilGetenvReadsProcessEnvironment(t *testing.T) {
	t.Setenv(EnvURL, testURL)
	t.Setenv(EnvWebsiteID, testID)
	got, err := LoadConfig(nil)
	if err != nil || !got.Enabled() {
		t.Fatalf("LoadConfig(nil) = %+v, %v", got, err)
	}
}

func TestZeroConfigIsOff(t *testing.T) {
	var c Config
	if err := c.Validate(); err != nil || c.Enabled() {
		t.Fatalf("zero config: Validate %v, Enabled %v", err, c.Enabled())
	}
}

func TestValidateRejectsNegativeValues(t *testing.T) {
	err := Config{Timeout: -time.Second, QueueSize: -1}.Validate()
	if err == nil {
		t.Fatal("negative values accepted")
	}
}
