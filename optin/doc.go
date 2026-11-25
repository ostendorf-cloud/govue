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
//   - The same status and body for a new, a pending, a confirmed and an
//     unsubscribed address. Every request runs the same two store updates,
//     and the mail is sent after the answer, so the SMTP time does not show.
//     The store updates still differ by a branch, see the concept doc.
//   - At most MaxSendsPerAddress confirmation mails per mailbox in a
//     counting period of PendingTTL, and ResendInterval between two, counted
//     in the store. The mailbox is the address in lower case with a "+tag"
//     at the end of the local part removed, so ada+1@ and Ada+2@ share one
//     budget. Dots, as Gmail ignores them, and other provider specific
//     aliases are not folded: such variants reach one inbox with a budget
//     each, and the real bound per inbox is then the global ceiling, 50 per
//     hour per process by default. The counter outlives an unsubscribe until
//     its period ends. A per client limit and the global ceiling come on top.
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
//     unconfirmed the hash of the newest confirmation token.
//   - Stored per mailbox, while a confirmation mail was sent in the last
//     PendingTTL: a send counter, keyed by a keyed hash (HMAC-SHA-256) of
//     the mailbox, with the number of confirmation mails, the time of the
//     last one and the start of the counting period. It holds no address,
//     but the site can recompute the hash from one, so it is pseudonymous
//     data.
//   - Not stored: no IP address, no user agent, no name, no referrer, no
//     cookie, no browser storage.
//   - Retention: an unconfirmed entry is deleted when its pending period
//     ends, 48 hours after the first request by default, at the next run of
//     Purge (RunPurge runs it every hour by default). A send counter goes
//     the same way when its counting period ends, also after an unsubscribe.
//     A confirmed entry stays until the person unsubscribes or the site
//     deletes it with [Handler.DeleteConfirmedBefore], at the retention the
//     site sets, for example 12 months after its launch mail. An unsubscribe
//     deletes the entry at once and keeps nothing of it, so a new request
//     enters the address again.
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
// table. The state column holds the values of [State]: 1 pending, 2
// confirmed, 3 counter.
//
//	CREATE TABLE waitlist (
//	    key             bytea PRIMARY KEY CHECK (octet_length(key) = 32),
//	    state           smallint NOT NULL CHECK (state IN (1, 2, 3)),
//	    address         text NOT NULL DEFAULT '',
//	    language        text NOT NULL DEFAULT '',
//	    consent_version text NOT NULL DEFAULT '',
//	    created         timestamptz NOT NULL,
//	    confirmed       timestamptz,
//	    confirm_hash    bytea UNIQUE CHECK (octet_length(confirm_hash) = 32),
//	    sends           integer NOT NULL DEFAULT 0,
//	    last_sent       timestamptz
//	);
//	CREATE INDEX waitlist_state_created ON waitlist (state, created);
//	CREATE INDEX waitlist_state_confirmed ON waitlist (state, confirmed);
//
// Update assumes READ COMMITTED, the PostgreSQL default, and runs in one
// transaction:
//
//	BEGIN;
//	SELECT ... FROM waitlist WHERE key = $1 FOR UPDATE;
//	-- fn runs in Go on the row, or on StateNone when there is none.
//	-- A row that fn left at StateNone:  DELETE FROM waitlist WHERE key = $1;
//	-- A row that fn changed:            UPDATE waitlist SET ... WHERE key = $1;
//	-- No row, and fn set a state:       INSERT INTO waitlist (...) VALUES (...);
//	COMMIT;
//
// The INSERT is a plain INSERT, never ON CONFLICT DO UPDATE. A missing row
// locks nothing, so two first requests for one key can both see none. The
// second INSERT then waits for the first transaction and fails with
// unique_violation (SQLSTATE 23505) once it commits. The adapter rolls back
// and runs the whole transaction again, fn included, a bounded number of
// times. The second run finds the row and locks it, so fn sees the first
// result, and the per mailbox limit holds. ON CONFLICT DO UPDATE would let
// the second overwrite the first without fn ever seeing it. A row deleted
// while another transaction waited on its lock comes back as no row, and
// the same INSERT and retry cover it. At REPEATABLE READ or SERIALIZABLE the
// adapter also retries on serialization_failure (SQLSTATE 40001). This
// reasoning follows the PostgreSQL documentation on row locks and unique
// indexes. It has not been run against a server.
//
// confirm_hash is NULL outside the pending state. DeleteUnconfirmedBefore is
// one DELETE with "state IN (1, 3) AND created <= $1", DeleteConfirmedBefore
// one with "state = 2 AND confirmed <= $1". EachConfirmed reads the keys
// first and closes the query before it calls fn. A store error must not
// carry an address or a token in its text, because the handler logs it.
//
// The limiters, the replay set and the mails in flight are per process.
// Wait lets a site finish the mails in flight at shutdown. This package
// imports no session and no database package.
package optin
