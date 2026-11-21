// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"syscall"

	"github.com/jackc/pgx/v5/pgconn"
)

// pingError is what Check returns when the ping fails. pgx' error quotes the
// host, the address and the user, and a host can carry a secret, so only the
// kind of the failure is kept. cause is a context error or an errno, whose
// text holds no address, and is nil otherwise.
type pingError struct {
	kind  string
	cause error
}

func (e *pingError) Error() string { return "database: ping: " + e.kind }

func (e *pingError) Unwrap() error { return e.cause }

// pingFailure maps err to a pingError. It never keeps err itself, and so
// never its text.
func pingFailure(err error) error {
	var (
		dns     *net.DNSError
		unknown x509.UnknownAuthorityError
		name    x509.HostnameError
		cert    x509.CertificateInvalidError
		verify  *tls.CertificateVerificationError
		alert   tls.AlertError
		record  tls.RecordHeaderError
		pg      *pgconn.PgError
		errno   syscall.Errno
		netErr  net.Error
	)
	switch {
	case errors.Is(err, context.Canceled):
		return &pingError{kind: "canceled", cause: context.Canceled}
	case errors.Is(err, context.DeadlineExceeded):
		return &pingError{kind: "timeout", cause: context.DeadlineExceeded}
	case pgconn.Timeout(err) || (errors.As(err, &netErr) && netErr.Timeout()):
		return &pingError{kind: "timeout"}
	case errors.As(err, &dns):
		return &pingError{kind: "host lookup failed"}
	case errors.As(err, &unknown), errors.As(err, &name), errors.As(err, &cert),
		errors.As(err, &verify), errors.As(err, &alert), errors.As(err, &record):
		return &pingError{kind: "tls failed"}
	case errors.As(err, &pg):
		// SQLSTATE class 28 is invalid authorization.
		if len(pg.Code) >= 2 && pg.Code[:2] == "28" {
			return &pingError{kind: "authentication failed"}
		}
		return &pingError{kind: "server refused"}
	case errors.Is(err, syscall.ECONNREFUSED):
		return &pingError{kind: "connection refused", cause: syscall.ECONNREFUSED}
	case errors.As(err, &errno):
		// An errno prints its own text and nothing of the address.
		return &pingError{kind: "connection failed", cause: errno}
	default:
		return &pingError{kind: "failed"}
	}
}
