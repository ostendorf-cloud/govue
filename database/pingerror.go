// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"syscall"

	"github.com/jackc/pgx/v5/pgconn"
)

// connError is a failure to reach or log in to the server, reduced to its
// kind. pgx' error quotes the host, the address and the user, and a host can
// carry a secret, so only the kind is kept. cause is a context error or an
// errno, whose text holds no address, and is nil otherwise.
type connError struct {
	kind  string
	cause error
}

func (e *connError) Error() string { return e.kind }

func (e *connError) Unwrap() error { return e.cause }

// connFailure maps err to a connError. It never keeps err itself, and so
// never its text.
func connFailure(err error) *connError {
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
		return &connError{kind: "canceled", cause: context.Canceled}
	case errors.Is(err, context.DeadlineExceeded):
		return &connError{kind: "timeout", cause: context.DeadlineExceeded}
	case pgconn.Timeout(err) || (errors.As(err, &netErr) && netErr.Timeout()):
		return &connError{kind: "timeout"}
	case errors.As(err, &dns):
		return &connError{kind: "host lookup failed"}
	case errors.As(err, &unknown), errors.As(err, &name), errors.As(err, &cert),
		errors.As(err, &verify), errors.As(err, &alert), errors.As(err, &record):
		return &connError{kind: "tls failed"}
	case errors.As(err, &pg):
		// SQLSTATE class 28 is invalid authorization.
		if len(pg.Code) >= 2 && pg.Code[:2] == "28" {
			return &connError{kind: "authentication failed"}
		}
		return &connError{kind: "server refused"}
	case errors.Is(err, syscall.ECONNREFUSED):
		return &connError{kind: "connection refused", cause: syscall.ECONNREFUSED}
	case errors.As(err, &errno):
		// An errno prints its own text and nothing of the address.
		return &connError{kind: "connection failed", cause: errno}
	default:
		return &connError{kind: "failed"}
	}
}

// pingFailure is the error of a failed ping.
func pingFailure(err error) error {
	return fmt.Errorf("database: ping: %w", connFailure(err))
}

// redactConnect keeps pgx' connect error, which quotes the host, out of an
// error that is logged or returned: a failed dial, whatever the pool call
// that made it, is reduced to its kind. Any other error is returned as it is.
func redactConnect(err error) error {
	var connect *pgconn.ConnectError
	if errors.As(err, &connect) {
		return connFailure(err)
	}
	return err
}
