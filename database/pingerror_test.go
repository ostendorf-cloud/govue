// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ostendorf-cloud/govue/internal/configtext/configtexttest"
)

// quoting is an error like pgx' connect error: its text quotes the host.
type quoting struct{ err error }

func (q quoting) Error() string {
	return "failed to connect to `host=db.example.invalid?password=s3cr3tTOKEN`: " + q.err.Error()
}

func (q quoting) Unwrap() error { return q.err }

// timeoutErr is a net.Error that timed out.
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout db.example.invalid?password=s3cr3tTOKEN" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return false }

func TestPingFailureNamesTheKindAndNotTheHost(t *testing.T) {
	t.Parallel()

	const host = "db.example.invalid?password=s3cr3tTOKEN"
	cases := []struct {
		name  string
		err   error
		want  string
		is    error
		isNot error
	}{
		{
			"refused",
			quoting{&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}},
			"connection refused", syscall.ECONNREFUSED, nil,
		},
		{
			"other errno",
			quoting{&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)}},
			"connection failed", syscall.EHOSTUNREACH, syscall.ECONNREFUSED,
		},
		{"dns", quoting{&net.DNSError{Err: "no such host", Name: host, IsNotFound: true}}, "host lookup failed", nil, nil},
		{"deadline", quoting{context.DeadlineExceeded}, "timeout", context.DeadlineExceeded, nil},
		{"net timeout", quoting{timeoutErr{}}, "timeout", nil, nil},
		{"canceled", quoting{context.Canceled}, "canceled", context.Canceled, nil},
		{"unknown authority", quoting{x509.UnknownAuthorityError{}}, "tls failed", nil, nil},
		{"wrong host name", quoting{x509.HostnameError{Host: host}}, "tls failed", nil, nil},
		{"password refused", quoting{&pgconn.PgError{Code: "28P01", Message: host}}, "authentication failed", nil, nil},
		{"other server error", quoting{&pgconn.PgError{Code: "57P03", Message: host}}, "server refused", nil, nil},
		{"unknown", quoting{errors.New(host)}, "failed", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := &DB{pinger: fakePinger{err: tc.err}}
			err := db.Check(t.Context())
			configtexttest.AssertHidden(t, err, "s3cr3tTOKEN", "database: ping: "+tc.want)
			for _, secret := range []string{"db.example.invalid", "5432", "user="} {
				if strings.Contains(fmt.Sprintf("%#v", err), secret) || strings.Contains(err.Error(), secret) {
					t.Errorf("error %q holds %q", err, secret)
				}
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("errors.Is(%v, %v) = false, want true", err, tc.is)
			}
			if tc.isNot != nil && errors.Is(err, tc.isNot) {
				t.Errorf("errors.Is(%v, %v) = true, want false", err, tc.isNot)
			}
		})
	}
}

func TestConfigLogValueLeavesTheHostOut(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Host: "db.example.invalid?password=s3cr3tTOKEN", Port: 5432, Name: "app", User: "app",
		Password: fakeSecret, SSLMode: SSLRequire,
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("config", "db", cfg)
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config", "db", cfg)
	got := buf.String()
	for _, secret := range []string{"s3cr3tTOKEN", "db.example.invalid"} {
		if strings.Contains(got, secret) {
			t.Errorf("log %q holds %q", got, secret)
		}
	}
	for _, want := range []string{"5432", "sslmode", "require", "[redacted]"} {
		if !strings.Contains(got, want) {
			t.Errorf("log %q lacks %q: the other fields must stay", got, want)
		}
	}
}
