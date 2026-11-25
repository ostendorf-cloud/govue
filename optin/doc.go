// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package optin is a waitlist with double opt-in: a request handler for the
// form, a confirm and an unsubscribe handler for the links in the mail, a
// purge of unconfirmed entries and one call that mails every confirmed
// entry.
//
// The flow. The form posts an address, the consent box and a proof of work
// to [Handler.Request]. The answer is 200 {"ok":true} for every address,
// known or not, and a confirmation mail goes out after the answer when one
// is due. The mail carries a confirmation link and an unsubscribe link to
// the site's own pages, which post the token from the query to
// [Handler.Confirm] or [Handler.Unsubscribe]. The List-Unsubscribe header
// points at [Handler.Unsubscribe] itself for the one click of RFC 8058.
//
// The rules, each enforced in code and covered by a test:
//
//   - An unconfirmed address receives the confirmation mail and nothing
//     else. Every mail leaves through one function that takes a recipient,
//     and only a pending entry yields a recipient for a confirmation, only
//     a confirmed entry one for a mailing.
//   - Every mail carries the unsubscribe link in its body and the
//     List-Unsubscribe and List-Unsubscribe-Post headers. A body without the
//     link is not sent, and New refuses a Confirmation function whose sample
//     body lacks it.
//   - The same status and body for a new, a pending, a confirmed and a
//     suppressed address. The mail is sent after the answer, so the SMTP
//     time does not show. The store update still differs by a branch, see
//     the concept doc.
//   - At most MaxSendsPerAddress confirmation mails per pending period and
//     ResendInterval between two, kept in the store. A per client limit and a
//     global ceiling per process come on top.
//   - The consent box must be true. The stored record is
//     Config.ConsentVersion and the times, the box itself is a claim of the
//     client.
//   - A confirmation token is 256 random bits, stored only as a SHA-256
//     hash, single use, valid for the pending period and compared in
//     constant time. An unsubscribe token is the entry key and its HMAC, not
//     stored, valid until the secret changes.
//
// Privacy facts for a site's own privacy text. This is a statement of fact,
// not legal advice.
//
//   - Stored per address, in the site's Store: the address as typed, the
//     state, the start of the pending period, the confirmation time, the
//     consent text version of the site, the page language hint, and while
//     unconfirmed the hash of the newest confirmation token, the number of
//     confirmation mails and the time of the last one.
//   - Not stored: no IP address, no user agent, no name, no referrer, no
//     cookie, no browser storage.
//   - Retention: an unconfirmed entry is deleted when its pending period
//     ends, 48 hours after the first request by default, at the next run of
//     Purge (RunPurge runs it every hour by default). A confirmed entry stays
//     until the person unsubscribes. An unsubscribe deletes the entry and
//     keeps a suppression marker: a keyed hash (HMAC-SHA-256) of the address
//     in lower case and nothing else, so the address is never mailed again.
//     The site can recompute the hash from an address, so it is pseudonymous
//     data. [Handler.Unsuppress] removes it on the person's request.
//   - In process memory: the limiter keys, which are the IPv4 address or the
//     IPv6 /64 of a client (up to 100000 keys per limiter, kept until the
//     bucket refills), and the replay set of the proof of work.
//   - Sent: the confirmation mail and the site's mailings, through the
//     site's mail.Sender, to the address only. The texts are the site's.
//     The package adds the links and the headers List-Unsubscribe,
//     List-Unsubscribe-Post and Auto-Submitted, and nothing about the
//     visitor.
//   - Logs hold codes, states and type names, never an address or a token.
//
// # A SQL store
//
// govue ships no SQL store: the product keeps its schema and its queries,
// and pgx stays in the database package. A PostgreSQL adapter can use this
// table:
//
//	CREATE TABLE waitlist (
//	    key             bytea PRIMARY KEY,
//	    state           smallint NOT NULL,
//	    address         text NOT NULL DEFAULT '',
//	    language        text NOT NULL DEFAULT '',
//	    consent_version text NOT NULL DEFAULT '',
//	    created         timestamptz,
//	    confirmed       timestamptz,
//	    confirm_hash    bytea UNIQUE,
//	    sends           integer NOT NULL DEFAULT 0,
//	    last_sent       timestamptz
//	);
//
// Update runs in one transaction: SELECT ... FOR UPDATE on the key, fn, then
// an INSERT ... ON CONFLICT (key) DO UPDATE, or a DELETE when fn left the
// state at StateNone. Two first requests for one address race on the
// INSERT: the loser retries the transaction once, so it sees the winner's
// row. confirm_hash is NULL outside the pending state. DeletePendingBefore
// is one DELETE with "state = 1 AND created <= $1". EachConfirmed reads the
// keys first and closes the query before it calls fn. A store error must
// not carry an address or a token in its text, because the handler logs it.
//
// The limiters, the replay set and the mails in flight are per process.
// Wait lets a site finish the mails in flight at shutdown. This package
// imports no session and no database package.
package optin
