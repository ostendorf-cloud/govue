// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package mail submits plain text mail over SMTP and builds the message so a
// visitor's input can never become a header.
//
// Build and SMTP.Send refuse a message instead of repairing it. CR, LF, NUL
// and every other control character in any header value or address is an
// error that wraps ErrInvalid and names the field, never the value. An
// address must be one plain ASCII mailbox: no list, no group, no quoted
// local part. Non ASCII subjects, display names and extra header values
// become RFC 2047 words, the body is UTF-8 in quoted-printable. Message-ID
// is random and on the sender's domain, Date is UTC. Sizes are limited, see
// MaxSubjectBytes and MaxBodyBytes. A visitor's address goes in
// Message.ReplyTo, never in From.
//
// SMTP connects to one submission server. By default it requires STARTTLS
// (port 587) and refuses a server without it before any credential leaves.
// ImplicitTLS (port 465) is an option, Plain is for a local test sink and
// takes no credentials. TLS is at least 1.2 and the certificate must name
// Config.Host. AUTH PLAIN or LOGIN is used only inside TLS. One timeout and
// the context bound the whole conversation. There are no retries, so a
// message is never sent twice by this package. An *Error carries the server's
// reply for the operator's log. It never goes to a client.
//
// What a consumer's privacy text can quote:
//
//   - The package stores nothing, sets no cookie and keeps no connection
//     between sends.
//   - The only network destination is the configured SMTP server.
//   - The message holds what the consumer puts in it, and nothing about the
//     visitor is added: no client IP, no user agent, no X-Mailer.
//   - EHLO sends Config.HeloName, "localhost" by default, never the host
//     name of the machine.
//   - No log line is written. Error text carries a stage and a reply code,
//     never an address, a name, the body or the password.
//
// The package imports envconf of this module and the standard library only.
package mail
