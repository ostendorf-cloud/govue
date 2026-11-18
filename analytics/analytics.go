// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package analytics counts page views on the server and reports them to
// Umami. No script runs in the browser, no cookie is set and nothing is read
// from the visitor's device beyond the request the browser sends anyway.
//
// Counting is off unless both UMAMI_URL and UMAMI_WEBSITE_ID are set. The
// Middleware of a Client records a view for a GET that answered 200 with an
// HTML page. It leaves out a visitor who sends Do Not Track or Global Privacy
// Control, a prefetch, a bot, a client on a private address and the health
// and metrics routes. The URL it reports is the route pattern of the mux,
// never the query string and never an id from the path.
//
// A view goes into a bounded queue that a fixed number of workers post to
// Umami. A full queue drops the view, so a slow or dead Umami never holds a
// response. Failures and drops are logged once per class and interval, never
// with the URL of Umami, and OnDrop lets the product count them.
//
// The package imports clientip and nothing else of this module. The product
// owns which routes count, the Umami values, the privacy text and the
// retention statement.
package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ostendorf-cloud/govue/clientip"
)

const (
	// workers is the fixed number of goroutines posting to Umami.
	workers = 2
	// logInterval is the most often a failure class is logged.
	logInterval = time.Minute
	// maxResponseBody bounds what is read of an answer from Umami before it
	// is discarded, so the connection can be reused.
	maxResponseBody = 4 << 10
)

// Options are the dependencies and the product's choices of a Client.
type Options struct {
	// Hostname is the public host of the site, sent as the hostname of every
	// view. It comes from the product, never from the Host header, which a
	// request controls. Required when counting is on.
	Hostname string
	// Logger receives the failure lines. Required when counting is on.
	Logger *slog.Logger
	// HTTPClient posts to Umami. It must honour the request context. Nil
	// builds a client with no cookie jar that refuses redirects, so a
	// visitor address never follows a redirect to another host.
	HTTPClient *http.Client
	// Now is the clock of the log interval. Nil is time.Now.
	Now func() time.Time
	// ClientIP resolves the client address. Nil uses the TCP peer only,
	// which behind a proxy is the proxy and so no public address.
	ClientIP *clientip.Resolver
	// Skip excludes more requests. The health and metrics paths are always
	// excluded, whatever Skip says.
	Skip func(*http.Request) bool
	// Title returns the page title to report. Nil reports none. A title may
	// hold user content, so the product decides.
	Title func(*http.Request) string
	// OnDrop is called once for every view that is lost: a full queue, or a
	// view still queued when Close ran out of time. It runs on the request
	// path and must not block.
	OnDrop func()
	// AllowPrivateClients counts a client on a private, loopback or shared
	// address too. It is for a development run. By default such a client is
	// the cluster, a VPN or a probe, not a visitor.
	AllowPrivateClients bool
}

// Stats counts what happened to the views handed to a Client.
type Stats struct {
	// Sent is the number of views Umami accepted.
	Sent uint64
	// Failed is the number of views Umami did not accept or could not be
	// reached for.
	Failed uint64
	// Dropped is the number of views lost to a full queue or to Close.
	Dropped uint64
}

// View is one page view as Umami receives it.
type View struct {
	// URL is the route pattern or the cleaned path, without query.
	URL string
	// Title is the page title, empty when the product reports none.
	Title string
	// Referrer is the origin of another site, empty otherwise.
	Referrer string
	// Language is the first language tag of Accept-Language.
	Language string
	// UserAgent is the visitor's user agent, which Umami reads the browser
	// and system from.
	UserAgent string
	// IP is the visitor's address, which Umami hashes itself.
	IP netip.Addr
}

// Client queues views and posts them to Umami. Build it with New and call
// Close on shutdown.
type Client struct {
	enabled   bool
	endpoint  string
	host      string
	websiteID string
	hostname  string
	timeout   time.Duration

	http     *http.Client
	logger   *slog.Logger
	now      func() time.Time
	resolver *clientip.Resolver
	skip     func(*http.Request) bool
	title    func(*http.Request) string
	onDrop   func()
	private  bool

	// mu makes close(queue) and the sends on it exclusive.
	mu     sync.RWMutex
	closed bool
	queue  chan View

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc

	sent, failed, dropped atomic.Uint64

	logMu   sync.Mutex
	classes map[string]*classState
}

// classState is the log bookkeeping of one failure class.
type classState struct {
	pending int
	logged  bool
	last    time.Time
}

// New validates cfg and opts and starts the workers. A configuration that
// counts nothing gives an inert Client whose Middleware returns the handler
// unchanged. Every problem is returned together.
func New(cfg Config, opts Options) (*Client, error) {
	problems := []error{cfg.Validate()}
	if !cfg.Enabled() {
		if err := errors.Join(problems...); err != nil {
			return nil, err
		}
		return &Client{}, nil
	}
	if opts.Hostname == "" || strings.ContainsAny(opts.Hostname, "/@?# \t") || strings.Contains(opts.Hostname, "://") {
		problems = append(problems, errors.New("analytics: Options.Hostname must be a bare host such as example.test"))
	}
	if opts.Logger == nil {
		problems = append(problems, errors.New("analytics: Options.Logger is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	base, err := url.Parse(cfg.URL)
	if err != nil {
		// Validate parsed it already; no error text, it would hold the URL.
		return nil, errors.New("analytics: " + EnvURL + " must be a URL")
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	size := cfg.QueueSize
	if size == 0 {
		size = DefaultQueueSize
	}

	c := &Client{
		enabled:   true,
		endpoint:  base.JoinPath("api", "send").String(),
		host:      base.Host,
		websiteID: cfg.WebsiteID,
		hostname:  opts.Hostname,
		timeout:   timeout,
		http:      opts.HTTPClient,
		logger:    opts.Logger,
		now:       opts.Now,
		resolver:  opts.ClientIP,
		skip:      opts.Skip,
		title:     opts.Title,
		onDrop:    opts.OnDrop,
		private:   opts.AllowPrivateClients,
		queue:     make(chan View, size),
		classes:   map[string]*classState{},
	}
	if c.http == nil {
		c.http = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.resolver == nil {
		c.resolver = clientip.NewResolver(nil)
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	for range workers {
		c.wg.Add(1)
		go c.worker()
	}
	return c, nil
}

// Enabled reports whether this client counts views.
func (c *Client) Enabled() bool { return c.enabled }

// Stats returns the counters so far.
func (c *Client) Stats() Stats {
	return Stats{Sent: c.sent.Load(), Failed: c.failed.Load(), Dropped: c.dropped.Load()}
}

// Track queues v without waiting. A full queue, or a closed client, drops it
// and calls OnDrop. Nothing here blocks the handler that calls it.
func (c *Client) Track(v View) {
	if !c.enabled {
		return
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		c.drop("shutdown")
		return
	}
	select {
	case c.queue <- v:
	default:
		c.drop("queue_full")
	}
}

// Close stops accepting views and waits for the queued ones to be posted. When
// ctx ends first, the posts in flight are cancelled, the rest are counted as
// dropped and Close returns the context error without waiting for the
// workers. It is safe to call more than once.
func (c *Client) Close(ctx context.Context) error {
	if !c.enabled {
		return nil
	}
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.queue)
	}
	c.mu.Unlock()

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		c.cancel()
		c.flushLog()
		return nil
	case <-ctx.Done():
		c.cancel()
		c.flushLog()
		return fmt.Errorf("analytics: close before the queue drained: %w", ctx.Err())
	}
}

func (c *Client) worker() {
	defer c.wg.Done()
	for v := range c.queue {
		c.deliver(v)
	}
}

func (c *Client) deliver(v View) {
	if c.ctx.Err() != nil {
		c.drop("shutdown")
		return
	}
	ctx, cancel := context.WithTimeout(c.ctx, c.timeout)
	defer cancel()
	if err := c.send(ctx, v); err != nil {
		// The line is written before the counter moves, so whoever sees the
		// count also finds the line.
		var se *sendError
		if errors.As(err, &se) {
			c.report(se.class)
		}
		c.failed.Add(1)
		return
	}
	c.sent.Add(1)
}

// sendError is a failed post. Its text names a class and never the URL.
type sendError struct {
	class string
	err   error
}

func (e *sendError) Error() string { return "analytics: post page view: " + e.class }
func (e *sendError) Unwrap() error { return e.err }

// event is the body of Umami's send endpoint.
type event struct {
	Type    string  `json:"type"`
	Payload payload `json:"payload"`
}

// payload carries ip and userAgent itself, which Umami prefers over the
// request's: the request comes from this server, not from the visitor.
type payload struct {
	Website   string `json:"website"`
	Hostname  string `json:"hostname"`
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	Referrer  string `json:"referrer,omitempty"`
	Language  string `json:"language,omitempty"`
	IP        string `json:"ip"`
	UserAgent string `json:"userAgent"`
}

func (c *Client) send(ctx context.Context, v View) error {
	body, err := json.Marshal(event{Type: "event", Payload: payload{
		Website:   c.websiteID,
		Hostname:  c.hostname,
		URL:       v.URL,
		Title:     v.Title,
		Referrer:  v.Referrer,
		Language:  v.Language,
		IP:        v.IP.String(),
		UserAgent: v.UserAgent,
	}})
	if err != nil {
		return &sendError{class: "encode", err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return &sendError{class: "request", err: errors.New("build request")}
	}
	req.Header.Set("Content-Type", "application/json")
	// Umami's bot check reads the agent. The payload's agent wins, this one
	// agrees with it.
	req.Header.Set("User-Agent", v.UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return &sendError{class: classify(err), err: stripURL(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &sendError{class: "status_" + strconv.Itoa(resp.StatusCode/100) + "xx", err: errors.New("unexpected status")}
	}
	return nil
}

// classify names the failure of a post without any detail of the URL.
func classify(err error) string {
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	}
	return "transport"
}

// stripURL returns the cause inside a *url.Error, which would repeat the
// request URL, with its path.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// drop counts one lost view and tells the product.
func (c *Client) drop(class string) {
	c.dropped.Add(1)
	if c.onDrop != nil {
		c.onDrop()
	}
	c.report(class)
}

// report logs the first failure of a class at once and the next ones at most
// once per logInterval, with the count since the last line. The line names
// the class, the count and the Umami host, never the URL, a query or a
// visitor value.
func (c *Client) report(class string) {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	st := c.classes[class]
	if st == nil {
		st = &classState{}
		c.classes[class] = st
	}
	st.pending++
	now := c.now()
	if st.logged && now.Sub(st.last) < logInterval {
		return
	}
	c.logLine(class, st, now)
}

// flushLog writes the counts still pending, so a shutdown loses none.
func (c *Client) flushLog() {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	now := c.now()
	for class, st := range c.classes {
		if st.pending > 0 {
			c.logLine(class, st, now)
		}
	}
}

// logLine writes one line. logMu is held.
func (c *Client) logLine(class string, st *classState, now time.Time) {
	c.logger.Warn("page views not counted", "class", class, "count", st.pending, "umami_host", c.host)
	st.pending, st.logged, st.last = 0, true, now
}
