#!/usr/bin/env python3
"""Cloudflare DNS-01 hook backend for Angie's native ACME (issue #13).

Angie's http_acme module has no built-in DNS provider, so for challenge=dns it
makes an internal request to an `acme_hook` location per add/remove. That
location proxy_passes here with the challenge variables as headers; this service
creates or deletes the _acme-challenge TXT record via the Cloudflare API.

Standard library only (no pip deps) so it runs on a stock python image with the
script bind-mounted — no custom image, no build pipeline.

Env:
  CF_API_TOKEN   scoped Cloudflare token (Zone:DNS:Edit on the zone) — a secret
  CF_ZONE        the DNS zone, e.g. example.com
  HOOK_PORT      listen port (default 9001; localhost only)
  HOOK_ADD_DELAY seconds to wait after creating a record so it is visible on
                 Cloudflare's authoritative NS before ACME validation (default 10)
"""
import json
import logging
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

CF_API = "https://api.cloudflare.com/client/v4"
TOKEN = os.environ.get("CF_API_TOKEN", "")
ZONE = os.environ.get("CF_ZONE", "")
PORT = int(os.environ.get("HOOK_PORT", "9001"))
ADD_DELAY = int(os.environ.get("HOOK_ADD_DELAY", "10"))

# Fail fast on a misconfigured deploy: an empty secret env would otherwise let
# the container come up "healthy" while every Cloudflare call is unauthorized.
if not TOKEN:
    raise SystemExit("acme-hook: CF_API_TOKEN is empty or unset")
if not ZONE:
    raise SystemExit("acme-hook: CF_ZONE is empty or unset")

logging.basicConfig(
    level=logging.INFO, format="%(asctime)s acme-hook %(levelname)s %(message)s"
)
log = logging.getLogger("acme-hook")


def cf(method, path, body=None):
    """One Cloudflare API v4 call; raises on a non-success response."""
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        CF_API + path,
        data=data,
        method=method,
        headers={
            "Authorization": "Bearer " + TOKEN,
            "Content-Type": "application/json",
        },
    )
    with urllib.request.urlopen(req, timeout=15) as resp:
        payload = json.load(resp)
    if not payload.get("success", False):
        raise RuntimeError("Cloudflare API error: " + json.dumps(payload.get("errors")))
    return payload["result"]


_ZONE_ID = None


def zone_id():
    # The zone id is stable for the life of the process; fetch it once instead
    # of on every request (one fewer round-trip and failure point per hook call).
    global _ZONE_ID
    if _ZONE_ID is None:
        result = cf("GET", "/zones?name=" + urllib.parse.quote(ZONE))
        if not result:
            raise RuntimeError("zone not found: " + ZONE)
        _ZONE_ID = result[0]["id"]
    return _ZONE_ID


def verify_zone():
    # Startup sanity check that catches a misconfigured CF_ZONE (e.g. a typo in
    # ACME_DOMAIN) early, instead of only when Angie first asks for a challenge.
    #
    # Fatal ONLY on a definitive answer: a SUCCESSFUL API call that returns no
    # matching zone means the zone truly does not exist under this token — a
    # permanent misconfiguration, so exit and let the container crash-loop with
    # a clear message (and, via the healthcheck, keep Angie from starting).
    #
    # A transport/API error (network blip, 5xx, rate-limit, token hiccup) is
    # NOT fatal: log and start anyway so a transient Cloudflare problem at boot
    # cannot take the whole stack down — zone_id() will retry lazily on the
    # first challenge.
    try:
        result = cf("GET", "/zones?name=" + urllib.parse.quote(ZONE))
    except Exception as exc:  # transient — do not couple startup to CF uptime
        log.warning("zone check skipped (transient error, will retry lazily): %s", exc)
        return
    if not result:
        raise SystemExit(
            "acme-hook: zone not found under this token: %s "
            "(check CF_ZONE / ACME_DOMAIN for a typo)" % ZONE
        )
    global _ZONE_ID
    _ZONE_ID = result[0]["id"]  # warm the cache while we are here
    log.info("zone verified: %s (%s)", ZONE, _ZONE_ID)


def record_name(domain):
    # Angie strips the leading '*.' from wildcard domains, so a wildcard cert
    # for *.example.com arrives here as domain=example.com.
    return "_acme-challenge." + domain


def _txt_records(zid, name):
    q = urllib.parse.urlencode({"type": "TXT", "name": name})
    return cf("GET", "/zones/%s/dns_records?%s" % (zid, q))


def add(zid, name, value):
    # Fresh challenge: clear any record left at this exact name by a crashed
    # prior run (the orphan-TXT accumulation, e.g. a leftover t5) so issuance
    # starts clean, then create the new one. Scoped to this challenge's own
    # record name — never a zone-wide delete.
    purge(zid, name)
    cf("POST", "/zones/%s/dns_records" % zid,
       {"type": "TXT", "name": name, "content": value, "ttl": 60})
    log.info("added TXT %s (waiting %ss for propagation)", name, ADD_DELAY)
    time.sleep(ADD_DELAY)


def purge(zid, name):
    # Delete every TXT at this _acme-challenge name; only called from add(),
    # where re-establishing the challenge makes clearing stale records correct.
    for rec in _txt_records(zid, name):
        cf("DELETE", "/zones/%s/dns_records/%s" % (zid, rec["id"]))
        log.info("purged stale TXT %s", name)


def remove(zid, name, value):
    # Post-validation cleanup: delete only the record matching this exact
    # value. An empty value is a caller bug, not a signal to wipe every record
    # at the name (that would DoS concurrent/other challenges).
    if not value:
        raise RuntimeError("refusing to remove with empty keyauth value")
    for rec in _txt_records(zid, name):
        if rec.get("content") == value:
            cf("DELETE", "/zones/%s/dns_records/%s" % (zid, rec["id"]))
            log.info("removed TXT %s", name)


class Handler(BaseHTTPRequestHandler):
    # Angie's internal acme_hook request arrives as a GET (observed live:
    # POST-only handling made Angie log "hook add: status code 404, renewal
    # aborted"), so the op handling is method-agnostic.
    def _handle(self):
        if self.path == "/healthz":
            return self._reply(200)
        op = self.headers.get("X-Acme-Op", "")
        domain = self.headers.get("X-Acme-Domain", "")
        keyauth = self.headers.get("X-Acme-Keyauth", "")
        try:
            if not domain:
                raise RuntimeError("missing X-Acme-Domain")
            # Only ever touch records inside our own zone — never let a caller
            # steer a challenge for an arbitrary host (e.g. the control plane).
            if domain != ZONE and not domain.endswith("." + ZONE):
                raise RuntimeError("domain outside zone %s: %s" % (ZONE, domain))
            name = record_name(domain)
            zid = zone_id()
            if op == "add":
                add(zid, name, keyauth)
            elif op == "remove":
                remove(zid, name, keyauth)
            else:
                raise RuntimeError("unknown op: " + op)
            self._reply(200)
        except Exception as exc:  # loud logging is the whole point (#13 risk)
            log.error("op=%s domain=%s failed: %s", op, domain, exc)
            self._reply(500)

    def do_GET(self):  # noqa: N802
        self._handle()

    def do_POST(self):  # noqa: N802
        self._handle()

    def _reply(self, code):
        self.send_response(code)
        self.end_headers()

    def log_message(self, *args):
        pass  # silence the default per-request stderr spam; we log our own


if __name__ == "__main__":
    verify_zone()
    log.info("listening on 127.0.0.1:%s for zone %s", PORT, ZONE)
    try:
        # Threaded so the blocking propagation sleep in add() cannot stall a
        # concurrent /healthz probe or a second challenge (wildcard + apex).
        ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
    except KeyboardInterrupt:
        sys.exit(0)
