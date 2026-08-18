// acme-hook — Cloudflare DNS-01 hook backend for Angie's native ACME (issue #13).
//
// Angie's http_acme module has no built-in DNS provider, so for challenge=dns it
// makes an internal request to an `acme_hook` location per add/remove. That
// location proxy_passes here with the challenge variables as headers; this
// service creates or deletes the _acme-challenge TXT record via the Cloudflare
// API.
//
// Standard library only — no module dependencies, so there is nothing to resolve
// at build time and nothing to keep patched afterwards. That was the property
// worth preserving from acme-hook.py, which stays in this directory as the
// fallback until this version has renewed a real certificate.
//
// Env:
//
//	CF_API_TOKEN   scoped Cloudflare token (Zone:DNS:Edit on the zone) — a secret
//	CF_ZONE        the DNS zone, e.g. example.com
//	HOOK_PORT      listen port (default 9001; localhost only)
//	HOOK_ADD_DELAY seconds to wait after creating a record so it is visible on
//	               Cloudflare's authoritative NS before ACME validation (default 10)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const cfAPI = "https://api.cloudflare.com/client/v4"

var (
	token    string
	zone     string
	addr     string
	addDelay time.Duration

	// 15s per call: long enough for a slow Cloudflare response, short enough
	// that one hung call cannot hold an ACME challenge open indefinitely.
	client = &http.Client{Timeout: 15 * time.Second}

	// The zone id is stable for the life of the process, so it is fetched once.
	// The Python original could keep it in a bare global because only one thread
	// runs at a time there; here request handlers run in genuine parallel, so
	// the cache needs a lock of its own.
	zoneMu sync.Mutex
	zoneID string
)

// mustEnv fails fast on a misconfigured deploy. An empty secret would otherwise
// let the container come up "healthy" while every Cloudflare call is
// unauthorized — a failure that surfaces 60 days later, at renewal.
func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("FATAL %s is empty or unset", name)
	}
	return v
}

// cf makes one Cloudflare API v4 call and returns its result field.
//
// The status code is not the verdict: Cloudflare reports failures in the body,
// sometimes alongside a 200, so success is what decides.
func cf(method, path string, body any) (json.RawMessage, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(encoded)
	}

	// Carries a context because every outbound call should be cancellable in
	// principle; the deadline that actually applies is the client timeout above.
	req, err := http.NewRequestWithContext(context.Background(), method, cfAPI+path, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var parsed struct {
		Success bool            `json:"success"`
		Errors  json.RawMessage `json:"errors"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("cloudflare %s %s: status %d, unreadable body: %w",
			method, path, resp.StatusCode, err)
	}
	if !parsed.Success {
		return nil, fmt.Errorf("cloudflare %s %s: status %d, errors: %s",
			method, path, resp.StatusCode, parsed.Errors)
	}
	return parsed.Result, nil
}

type zoneRecord struct {
	ID string `json:"id"`
}

func lookupZone() ([]zoneRecord, error) {
	result, err := cf("GET", "/zones?name="+url.QueryEscape(zone), nil)
	if err != nil {
		return nil, err
	}
	var zones []zoneRecord
	if err := json.Unmarshal(result, &zones); err != nil {
		return nil, err
	}
	return zones, nil
}

func zoneIdentifier() (string, error) {
	zoneMu.Lock()
	defer zoneMu.Unlock()
	if zoneID != "" {
		return zoneID, nil
	}
	zones, err := lookupZone()
	if err != nil {
		return "", err
	}
	if len(zones) == 0 {
		return "", fmt.Errorf("zone not found: %s", zone)
	}
	zoneID = zones[0].ID
	return zoneID, nil
}

// verifyZone is a startup sanity check that catches a misconfigured CF_ZONE
// (e.g. a typo in ACME_DOMAIN) early, instead of only when Angie first asks for
// a challenge. Its two outcomes are deliberately different:
//
//   - a SUCCESSFUL call returning no matching zone means the zone truly does not
//     exist under this token — a permanent misconfiguration, so exit and let the
//     container crash-loop with a clear message (and, via the healthcheck, keep
//     Angie from starting);
//   - a transport or API error (network blip, 5xx, rate-limit, token hiccup) is
//     NOT fatal: log and start anyway, so a transient Cloudflare problem at boot
//     cannot take the whole stack down. zoneIdentifier retries lazily on the
//     first challenge.
func verifyZone() {
	zones, err := lookupZone()
	if err != nil {
		log.Printf("WARN zone check skipped (transient error, will retry lazily): %v", err)
		return
	}
	if len(zones) == 0 {
		log.Fatalf("FATAL zone not found under this token: %s "+
			"(check CF_ZONE / ACME_DOMAIN for a typo)", zone)
	}
	zoneMu.Lock()
	zoneID = zones[0].ID
	zoneMu.Unlock()
	log.Printf("INFO zone verified: %s (%s)", zone, zones[0].ID)
}

type dnsRecord struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

func txtRecords(zid, name string) ([]dnsRecord, error) {
	q := url.Values{"type": {"TXT"}, "name": {name}}
	result, err := cf("GET", "/zones/"+zid+"/dns_records?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var records []dnsRecord
	if err := json.Unmarshal(result, &records); err != nil {
		return nil, err
	}
	return records, nil
}

// recordName mirrors Angie stripping the leading "*." from wildcard domains, so
// a wildcard cert for *.example.com arrives here as domain=example.com.
func recordName(domain string) string {
	return "_acme-challenge." + domain
}

// purge deletes every TXT at this _acme-challenge name. Only ever called from
// add, where re-establishing the challenge makes clearing stale records correct.
func purge(zid, name string) error {
	records, err := txtRecords(zid, name)
	if err != nil {
		return err
	}
	for _, rec := range records {
		if _, err := cf("DELETE", "/zones/"+zid+"/dns_records/"+rec.ID, nil); err != nil {
			return err
		}
		log.Printf("INFO purged stale TXT %q", name)
	}
	return nil
}

func add(zid, name, value string) error {
	// Fresh challenge: clear any record left at this exact name by a crashed
	// prior run (the orphan-TXT accumulation) so issuance starts clean, then
	// create the new one. Scoped to this challenge's own record name — never a
	// zone-wide delete.
	if err := purge(zid, name); err != nil {
		return err
	}
	body := map[string]any{"type": "TXT", "name": name, "content": value, "ttl": 60}
	if _, err := cf("POST", "/zones/"+zid+"/dns_records", body); err != nil {
		return err
	}
	log.Printf("INFO added TXT %q (waiting %s for propagation)", name, addDelay)
	time.Sleep(addDelay)
	return nil
}

func remove(zid, name, value string) error {
	// Post-validation cleanup: delete only the record matching this exact value.
	// An empty value is a caller bug, not a signal to wipe every record at the
	// name — that would DoS concurrent challenges (wildcard and apex run
	// together).
	if value == "" {
		return fmt.Errorf("refusing to remove with empty keyauth value")
	}
	records, err := txtRecords(zid, name)
	if err != nil {
		return err
	}
	for _, rec := range records {
		if rec.Content != value {
			continue
		}
		if _, err := cf("DELETE", "/zones/"+zid+"/dns_records/"+rec.ID, nil); err != nil {
			return err
		}
		log.Printf("INFO removed TXT %q", name)
	}
	return nil
}

// isDNSName reports whether s contains only what a DNS name may contain. Case
// is allowed through unchanged: the zone comparison below is case-sensitive, so
// a differently-cased name is refused there rather than silently normalised.
func isDNSName(s string) bool {
	if len(s) > 253 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

// checkDomain keeps a caller from steering a challenge at a name this node has
// no business proving — the control plane, say. It is a separate function so it
// can be tested without a Cloudflare round-trip: the decision and the I/O are
// worth keeping apart, and this guard is one of the three security properties
// of the whole service.
func checkDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("missing X-Acme-Domain")
	}
	// The domain arrives in a header and ends up in a log line and in a
	// Cloudflare record name, so it is constrained to what a DNS name may
	// contain. Without this, a newline in the header forges log entries — the
	// zone check alone does not stop it, since "evil
FAKE.example.com" is still
	// inside the zone as far as a suffix test is concerned.
	if !isDNSName(domain) {
		return fmt.Errorf("domain is not a DNS name: %q", domain)
	}
	if domain != zone && !strings.HasSuffix(domain, "."+zone) {
		return fmt.Errorf("domain outside zone %s: %s", zone, domain)
	}
	return nil
}

func dispatch(op, domain, keyauth string) error {
	// Everything that can be refused without talking to anyone is refused first.
	// The op used to be checked after the zone lookup, which meant a malformed
	// request cost a Cloudflare round-trip before being thrown away — and made
	// the guard untestable without credentials.
	if op != "add" && op != "remove" {
		return fmt.Errorf("unknown op: %s", op)
	}
	if err := checkDomain(domain); err != nil {
		return err
	}

	zid, err := zoneIdentifier()
	if err != nil {
		return err
	}
	name := recordName(domain)
	if op == "add" {
		return add(zid, name, keyauth)
	}
	return remove(zid, name, keyauth)
}

// handle answers any method. Angie's internal acme_hook request arrives as a GET
// — observed live: POST-only handling made Angie log "hook add: status code 404,
// renewal aborted" — so the op handling is method-agnostic.
//
// Nothing is logged per request by default here, which matches the Python
// version silencing BaseHTTPRequestHandler's stderr spam: the only lines in the
// log are the ones written below.
func handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		return
	}
	op := r.Header.Get("X-Acme-Op")
	domain := r.Header.Get("X-Acme-Domain")
	if err := dispatch(op, domain, r.Header.Get("X-Acme-Keyauth")); err != nil {
		// Loud logging is the whole point (#13 risk): a silent hook failure
		// surfaces only as a certificate that quietly stopped renewing.
		log.Printf("ERROR op=%q domain=%q failed: %v", op, domain, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// healthcheck is what the container healthcheck runs. The image is distroless:
// it carries no shell and no interpreter, so nothing outside this binary can
// probe the port — the Python version could call `python3 -c ...` only because
// its image happened to contain a whole interpreter.
//
// It deliberately does NOT require the token: a missing secret must stay a fatal
// startup error with a clear message, not degrade into an unhealthy container.
func healthcheck() {
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		log.Printf("ERROR healthcheck: %v", err)
		os.Exit(1)
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		log.Printf("ERROR healthcheck: %v", err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		log.Printf("ERROR healthcheck: status %d", resp.StatusCode)
		os.Exit(1)
	}
	os.Exit(0)
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	log.SetPrefix("acme-hook ")

	port := os.Getenv("HOOK_PORT")
	if port == "" {
		port = "9001"
	}
	// Bound to the loopback address explicitly, not to every interface: the hook
	// holds a Cloudflare token and has no authentication of its own, so being
	// unreachable is the only thing keeping it private.
	addr = "127.0.0.1:" + port

	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		healthcheck()
	}

	token = mustEnv("CF_API_TOKEN")
	zone = mustEnv("CF_ZONE")

	addDelay = 10 * time.Second
	if v := os.Getenv("HOOK_ADD_DELAY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			log.Fatalf("FATAL HOOK_ADD_DELAY is not a number: %q", v)
		}
		addDelay = time.Duration(n) * time.Second
	}

	verifyZone()

	// Each request is served in its own goroutine, so the propagation sleep in
	// add cannot stall a concurrent /healthz probe or a second challenge. That
	// is what ThreadingHTTPServer was doing in the Python version.
	srv := &http.Server{
		Addr:    addr,
		Handler: http.HandlerFunc(handle),
		// Only the header timeout is set. A WriteTimeout would have to exceed
		// HOOK_ADD_DELAY, since add deliberately holds the response open while
		// the record propagates — an easy way to break renewal by tidiness.
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("INFO listening on %s for zone %s", addr, zone)
	log.Fatal(srv.ListenAndServe())
}
