// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ostendorf-cloud/govue/contact"
	"github.com/ostendorf-cloud/govue/mail"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/pow"
)

// contactSendTimeout is below the 30 second write timeout and the 20 second
// shutdown timeout of httpserver, so a send ends before the answer can no
// longer be written and before a shutdown cuts it off.
const contactSendTimeout = 15 * time.Second

// contactPrefix is where the contact routes live.
const contactPrefix = "/api/v1/contact"

// fakeSender is the example's mail.Sender. It opens no connection and keeps
// the last message in memory, so the example runs and tests without an SMTP
// server. A product passes a *mail.SMTP from mail.NewSMTP(mail.LoadConfig(...))
// instead, and leaves the Sender nil while SMTP is not configured: the form
// then answers 503 and the process still starts.
type fakeSender struct {
	mu   sync.Mutex
	last mail.Message
	n    int
}

func (f *fakeSender) Send(_ context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = m
	f.n++
	return nil
}

// buildContact wires the contact form. Without CONTACT_POW_KEY the handlers
// are still built and answer 503 not_configured, so the example starts
// without the key. The sender is the in memory fake above.
func (a *app) buildContact() error {
	var service *pow.Service
	if len(a.cfg.contactKey) > 0 {
		var err error
		// The audience is the site name: a challenge of another site, even
		// one signed with the same key, is refused.
		service, err = pow.New(pow.Config{Audience: a.cfg.siteHost, Keys: [][]byte{a.cfg.contactKey}, Now: a.cfg.now})
		if err != nil {
			return fmt.Errorf("example: contact proof of work: %w", err)
		}
	}
	a.contactSender = &fakeSender{}
	handlers, err := contact.New(contact.Config{
		PoW:    service,
		Sender: a.contactSender,
		// The envelope is fixed by the site and never comes from a request.
		From: a.cfg.contactFrom,
		To:   a.cfg.contactTo,
		// The same resolver and origins as the rest of the example. The
		// limiters are the package defaults: 5 per 10 minutes per client,
		// 50 per hour for the whole process, 10 per 20 seconds for the
		// challenge route. All of them are per process.
		ClientIP:       a.clientIPs,
		TrustedOrigins: a.cfg.allowedOrigins,
		Logger:         a.logger,
		SendTimeout:    contactSendTimeout,
		Now:            a.cfg.now,
	})
	if err != nil {
		return fmt.Errorf("example: contact handlers: %w", err)
	}
	a.contact = handlers
	return nil
}

// mountContact registers the contact routes. They sit outside the route
// chain on purpose: the form is stateless, so there is no session to read,
// no CSRF token to check and no cookie to set, and the session middleware
// would answer a stray Authorization header with 401. The handlers check the
// origin themselves. The general rate limit stays in front, and the outer
// chain still applies.
func (a *app) mountContact(mux *http.ServeMux) {
	// CORS first, like every other route of the example: TrustedOrigins is set
	// from ALLOWED_ORIGINS, so a cross origin page may send mail and must also
	// be allowed to read the answer, or it retries and sends it twice. It also
	// answers the preflight.
	chain := func(h http.Handler) http.Handler {
		return middleware.Chain(h, a.corsMiddleware(), a.rateLimit.Middleware)
	}
	mux.Handle("GET "+contactPrefix+"/challenge", chain(a.contact.Challenge()))
	mux.Handle("POST "+contactPrefix, chain(a.contact.Submit()))
}
