# Stack: remnanode

🇬🇧 English · [🇷🇺 Русский](README.ru.md)

Remnawave node + an Angie reality-fallback proxy, with its TLS certificate
issued by Angie itself over ACME DNS-01 — no certbot on the host.

## Deploy

Via Dockhand → Stacks → **From Git**, path `remnanode`, into the target node's
environment.

## Environment variables (set per node in Dockhand)

| Variable | Secret | Purpose |
|---|---|---|
| `SECRET_KEY` | **yes** | node key from the Remnawave panel |
| `CF_API_TOKEN` | **yes** | DNS token the ACME hook uses to answer the DNS-01 challenge |
| `ACME_DOMAIN` | no | the parent zone (e.g. `example.com`); required — deploy fails without it |
| `NODE_NAME` | no | this node's short name (e.g. `pl`); required |
| `CAMO_SITE` | no | which decoy site to serve; assigned per node by the provisioning pipeline, `converter` when unset |
| `XHTTP_PATH` | **yes** | secret path of the xHTTP location (see [xHTTP](#xhttp)); set per node by the provisioning pipeline from OpenBao; empty = xHTTP off on this node |
| `XHTTP_UPSTREAM` | no | how Angie reaches the xHTTP inbound: `grpc` (default, `grpc_pass`) or `http` (`proxy_pass`, unbuffered) — for comparing the two on one node; anything else fails the deploy |

`ACME_DOMAIN` and `NODE_NAME` combine into `server_name
<NODE_NAME>.<ACME_DOMAIN>`, so **each node issues a certificate for its own
name** instead of sharing a fleet-wide wildcard — one node's compromised token
then cannot mint a certificate valid for the others.

Both are declared with `:?`, so a missing value fails the deploy immediately
rather than rendering a broken `server_name`.

## Certificates

Angie's native ACME client (`acme letsencrypt`) issues and renews directly;
`ssl_certificate` reads `$acme_cert_letsencrypt` from the `angie-acme` volume.
That volume holds the ACME **account key and the issued certificates** — keep
it across redeploys, or every deploy burns a duplicate-certificate slot.

Do not clear it on a live node: the config serves the certificate *from* the
volume, so an empty volume means no TLS until issuance completes.

The same volume is mounted **read-only** into `remnanode` at `/etc/xray-tls`,
for Hysteria2: QUIC needs real TLS for the node's own name, which is exactly
the certificate Angie keeps here. Xray re-reads the files hourly, so renewals
by Angie need no restart. The path is deliberately one that does not exist in
the panel container — the panel inlines certificate files it finds on its own
filesystem, private key included, into the config it pushes to nodes.

**One certificate per node, for its own name** (`NODE_NAME.ACME_DOMAIN`), not a
fleet-wide wildcard: a node's key is valid only for that node's name, so a
seized node cannot impersonate the panel or another node, and distinct names
keep Let's Encrypt's duplicate-certificate limit from tying the nodes together.

## The DNS-01 hook

Angie has no built-in DNS provider, so the challenge is answered by a small
service in `acme-hook/`. It creates and deletes the `_acme-challenge` TXT record
through the Cloudflare API, and it is the container that holds `CF_API_TOKEN`.

It exists twice on purpose:

| | |
|---|---|
| `acme-hook/` | **deployed.** A Go port built into a `distroless/static` image — one static binary and the CA certificates it needs |
| `acme-hook.py` | the original, kept as the way back. A stock `python:3.14-slim` image with the script bind-mounted |

The reason for the port is the image, not the language. `python:3.14-slim`
carries an interpreter, a package manager, a shell and a userland; a static Go
binary in distroless carries none of them. For the one container on the node
holding a Cloudflare token, that difference is the whole argument — the Go
version brings no dependencies of its own, so nothing else changes.

The cost is a build pipeline where there was none. CI compiles, lints, scans for
known vulnerabilities and runs the tests on every pull request touching
`acme-hook/`, then publishes to `ghcr.io/nargothrondir/acme-hook` on merge and
prints the digest to pin. The compose file pins that digest, exactly as it pinned
the Python image before.

**`acme-hook.py` stays until the Go version has renewed a real certificate.**
Renewal happens at 60 days, so a fault would otherwise surface long after the
change that caused it. Reverting is a one-block edit of the compose file.

### Exercising it without waiting for a renewal

The hook is driven entirely by request headers, so it can be called directly —
no ACME involved. On the node, against the running container:

```bash
curl -si -X GET http://127.0.0.1:9001/healthz | head -1
```

```bash
curl -si -X GET http://127.0.0.1:9001/   -H 'X-Acme-Op: add'   -H 'X-Acme-Domain: <the node name>.<the zone>'   -H 'X-Acme-Keyauth: test-value-please-delete' | head -1
```

A `200` and a TXT record appearing in Cloudflare at
`_acme-challenge.<the node name>.<the zone>` means add works; the same call with
`X-Acme-Op: remove` and the identical keyauth value must make it disappear.
Anything outside the zone must be refused — that guard is what stops a caller
steering a challenge at a name the node has no business proving.

## angie.conf

[`angie.conf`](angie.conf) holds directives only; the reasons are here,
section for section in the config's order.

### How the file is used

- **Mounted** as `/etc/angie/templates/http.d/default.conf` and included inside
  the image's `http {}` block — everything in it is http-level or a server
  block.
- **Rendered by gomplate at container start** (the `-templated` image). A
  change therefore reaches a node only when `remnawave-angie` restarts — see
  [Changing this file](#changing-this-file).
- **Template syntax is gomplate's double curly braces**, and gomplate parses
  them inside `#` comments too: an empty pair in a comment fails the render
  (`missing value for command`). Describe the syntax in words.
- **Template inputs** are read once, at the top of the file:

  | Variable | Required | Effect |
  |---|---|---|
  | `NODE_NAME`, `ACME_DOMAIN` | yes — the render fails without them | `server_name` = `NODE_NAME.ACME_DOMAIN`; the certificate is issued for it |
  | `XHTTP_PATH` | no | renders the xHTTP location; empty = xHTTP off on this node |
  | `XHTTP_UPSTREAM` | no, default `grpc` | `grpc` or `http`; anything else fails the render |

Traffic through the file:

```
:443/tcp  xray REALITY ──(not a REALITY client)──▶ unix:/dev/shm/nginx.sock
              ├─ SNI = this node's name ─▶ [4] decoy site
              │                               └─ XHTTP_PATH ─▶ xray xHTTP inbound
              └─ any other SNI ──────────▶ [5] handshake refused
127.0.0.1:9000 ─▶ [6] ACME DNS-01 hook ─▶ acme-hook service
```

### 1. Server identity

`server_tokens off` removes the version from the `Server` header and from error
pages. Replacing the name itself takes Angie PRO — the open-source build
accepts `on | off | build` only — so `more_set_headers "Server: nginx"` does it
instead, through the headers-more module. The image ships the module and loads
it when `ANGIE_LOAD_MODULES` names it (set in `docker-compose.yml`; the image's
main-config template is `/etc/angie/templates/angie.conf`, not the
`ANGIE_CONFIG_TEMPLATE` path its environment still names).

Why: Angie is rare outside Russia and nginx is the most common web server
there is, so "Server: Angie" on a foreign VPS is a small but free signal; Angie
is an nginx fork and behaves like one on the wire. The header is set on every
response, error pages included. The bodies of Angie's own error pages still
carry its name — the 404 is replaced by the site's page (section 4); other
error pages are not reachable from outside in normal operation.

`server_names_hash_bucket_size 64` leaves room for long node names.

### 2. TLS

The TLS block in `angie.conf` is shared by every listener, and on this node it
is more than the decoy site's encryption: **it is what every TCP client of the
node is seen doing.**

- REALITY uses its target's handshake as its camouflage, and the target here is
  this Angie. If the target supports `X25519MLKEM768`, REALITY clients that
  offer it use it too (Xray docs, REALITY `target`). Mihomo strips that group
  from its REALITY ClientHello unless `support-x25519mlkem768` is set
  (`component/tls/reality.go`, v1.19.31), so for Mihomo users REALITY stays on
  X25519 either way.
- xHTTP clients, browsers and probes finish their TLS here.

So the aim is to look like an ordinary, current web server, and the profile is
the one most such servers are configured from: **Mozilla "intermediate",
guidelines 6.0** (ssl-config.mozilla.org). Relative to the 5.x profile the config
carried before:

- `X25519MLKEM768` first among the groups — the hybrid post-quantum exchange
  current browsers offer. Needs OpenSSL 3.5 or newer; the image has 3.5.x.
- No `DHE-RSA-*` suites. They were never used anyway: DHE needs `ssl_dhparam`,
  which is not set.
- `ssl_prefer_server_ciphers off`: every suite on the list is strong, so the
  client picks — which is also what a stock configuration does.
- No OCSP stapling: Let's Encrypt shut its OCSP service down on 2025-08-06 and
  publishes revocation through CRLs only.

`ssl_session_tickets off` and the `MozSSL` session cache are carried over from
the same profile.

### 3. ACME client

Let's Encrypt, DNS-01 through the hook in [section 6](#6-acme-dns-01-hook), so no
inbound port is opened. State — the account key and the certificates — lives in
the `angie-acme` volume; see [Certificates](#certificates),
also for why `remnanode` mounts the same volume read-only for Hysteria2.

**No `resolver` directive, on purpose.** Angie 1.12 reads `/etc/resolv.conf`
itself and watches it for changes, and this container runs with
`network_mode: host` — so that is the host's file, the same encrypted resolver
apt, NetBird and everything else on the node uses. A hardcoded
`resolver 1.1.1.1 8.8.8.8` made the ACME client a second, independent DNS path
that ignored the node's configuration and asked in plaintext — for exactly the
lookups that reveal which name is getting a certificate and when. Verified on
the wire (lab node, 2026-08-10): with the directive removed and the ACME state
wiped, the whole issuance sent 16 packets to the local stub and 62 to the
upstream on 853, none to 1.1.1.1:53 or 8.8.8.8:53.

### 4. Node virtual host

The server for this node's own name: its certificate (issued and renewed by
section 3), the decoy site ([Reality camouflage](#reality-camouflage)), and —
when `XHTTP_PATH` is set — the xHTTP location ([xHTTP](#xhttp)).

| Line | Why |
|---|---|
| `set_real_ip_from unix:` + `real_ip_header proxy_protocol` | every connection arrives over the unix socket, so without this the access log records `unix:` for every request. With it, `$remote_addr` is the client address from the PROXY header REALITY sent, and the decoy's log shows who scans and probes the node. Only unix-socket peers are trusted (the special value `unix:` of the realip module); the ACME hook's loopback TCP listener is not affected. Users never appear in this log: REALITY traffic does not reach Angie, and the xHTTP location does not log |
| `Strict-Transport-Security "max-age=63072000"` | what an HTTPS-only site sends (Mozilla guidelines: two years). The node has no port 80 at all, so there is nothing to break |
| `Alt-Svc: h3=":443"; ma=86400` | Hysteria2 listens on UDP 443 and answers HTTP/3 without its password with this same site (masquerade), so the node does speak HTTP/3 — and a site that does announces it this way (RFC 9114 §3.1; the usual line in nginx configs with HTTP/3). Browsers then reach the decoy over h3 too. Unconditional because every node runs Hysteria2; a node that briefly does not (just provisioned, before "Enable Hysteria2") loses nothing — a browser whose QUIC attempt fails falls back to TCP |
| `X-Robots-Tag` | keeps the decoy out of search engines |
| `location /` → `try_files $uri $uri/ =404` | the decoy is static: a file, a directory index, or not found |
| `error_page 404 /index.html` | none of the bundled sites has a 404 page, so an unknown path used to get Angie's stock page with its name in the body. Now it gets the site's own page, still with status 404 — what many small sites do |
| `gzip` for CSS, JS, JSON, SVG (and HTML, always included) | ordinary sites compress text, and the decoy loads faster. Only inside `location /`: the xHTTP stream is never compressed, compression would buffer it |

The xHTTP location, line by line:

| Line | Why |
|---|---|
| `grpc_pass unix:/dev/shm/xhttp.sock` | HTTP/2 to the inbound's socket, request body streamed as it arrives. Same shape as XTLS/Xray-examples `VLESS-XHTTP3-Nginx` |
| `grpc_read_timeout` / `grpc_send_timeout 1h` | Xray closes idle connections at 300 s and watches **both** directions; Angie watches one stream at a time. At the example's 315 s it cut a download stream that stayed silent while the client kept uploading (`upstream timed out (110) while reading upstream`, 2026-09-23). An hour leaves the decision to Xray |
| `client_max_body_size 0` | a packet-up POST is up to 1 000 000 bytes (`scMaxEachPostBytes`), right at the 1m default |
| `client_body_timeout 5m` | a slow client's POST is not cut mid-body |
| `X-Real-IP` / `X-Forwarded-For` from `$proxy_protocol_addr` | the client address from the PROXY header REALITY sent. Xray honours `X-Forwarded-For` only when a header named in the inbound's `sockopt.trustedXForwardedFor` (`X-Real-IP`) is present; both are overwritten, so a client cannot forge them |
| `access_log off` | in packet-up every upload chunk is its own POST, its `Referer` padded with 100–1000 bytes: ~1 MB of log in three minutes of one client (measured 2026-09-23), every line holding the secret path and a timeline of who moved how much. The decoy site keeps its log |

`upstream prematurely closed connection` in the error log is the normal end of
an idle xHTTP stream: Xray ends it on its own idle timeout, and xHTTP is not
gRPC, so it sends no trailers.

### 5. Catch-all virtual host

The catch-all server (`default_server`) refuses any handshake whose SNI is not
this node's name, before a certificate is shown. It also closes (`444`) a
request whose `Host` names no server — which can arrive after a handshake on
this node's name, and would otherwise be answered by the catch-all.

### 6. ACME DNS-01 hook

A loopback-only server the ACME client calls for each add and remove of the
`_acme-challenge` TXT record; the request goes on as headers to the `acme-hook`
service, which holds the DNS token. How the hook works and how to test it:
[The DNS-01 hook](#the-dns-01-hook).

### Changing this file

Measured 2026-08-19, and it is the least obvious thing about this stack.

`angie.conf` is bind-mounted into the templates directory, and the `-templated`
image renders it with gomplate **at container start**:

```
./angie.conf → /etc/angie/templates/http.d/default.conf → (gomplate) → /etc/angie/…
```

So editing it and redeploying does nothing visible. Dockhand writes the new file
to disk, `docker compose up` compares the service definition, finds it unchanged
and leaves the container running — and inside that container the rendered
configuration is still the old one, because gomplate has not run since it
started.

The deploy reports success. The node keeps its previous behaviour.

**Restart the container to apply a configuration change:**

```bash
docker restart remnawave-angie
```

`angie -s reload` is not enough: it re-reads the *rendered* file, which is the
stale one.

A change to `docker-compose.yml` does not have this problem — that is a change
to the service definition, so compose recreates the container and gomplate runs.
Only config-file edits are affected, which is exactly the case where nothing
looks wrong.

CI renders the file through Angie's `templated` image for every variant of the optional
inputs — no xHTTP, xHTTP over `grpc`, xHTTP over `http` — runs `angie -t` on
each, and checks that `XHTTP_UPSTREAM=bogus` fails the render.

## Forcing a certificate renewal, for testing

`acme_client` takes a `renew_on_load` flag: *"the certificate should be forcibly
renewed each time the configuration is loaded."* Add it, restart the container,
and issuance runs immediately — through the DNS-01 hook, end to end.

Nothing needs to be wiped. The account key and the current certificate stay in
the `angie-acme` volume and the new certificate arrives over them, so there is
no window without TLS.

Three things to know before using it:

- **It fires on every configuration load, not once.** Used on 2026-08-19 it
  produced two certificates seven minutes apart, because the configuration was
  loaded twice. Let's Encrypt allows five duplicate certificates per week for one
  name set.
- **Remove it from a branch cut from `main`.** The first revert was branched from
  the branch that added the flag; squash-merging collapsed both commits into one
  with no net change, the pull request merged green, and the flag stayed live.
- **Removing it needs the same restart** as adding it. That restart is safe: it
  renders the flag-free configuration, so it does not issue.

Verified this way on 2026-08-19: the certificate moved from 2026-08-12 to
2026-08-19 02:01 UTC. A certificate cannot be re-issued without the DNS-01
record, and that record is created by the Go hook — which is how Angie calling
the hook was proven rather than assumed.

## xHTTP

A second way in, next to reality: VLESS over xHTTP, sharing port 443.

```
client ──TLS──▶ :443 xray reality ──(not a reality handshake)──▶ Angie (nginx.sock)
                                        ├─ XHTTP_PATH ──grpc_pass──▶ xray xHTTP inbound (xhttp.sock)
                                        └─ anything else ──────────▶ decoy site
```

The client does ordinary TLS with this node's own certificate. Reality relays
that handshake to Angie exactly as it relays a browser, so the node exposes no
new port and the xHTTP traffic arrives as HTTPS to the decoy site's name.

- **Enabled per node** by `XHTTP_PATH`. Unset, the location is not rendered
  and the node behaves as before; that is what lets a merge here reach the
  fleet without switching xHTTP on anywhere.
- **The path is a secret.** It is the one request that answers differently
  from the decoy site, so it is stored in OpenBao and set as a Dockhand secret.
- **The inbound lives in the Remnawave config profile**, not here: it listens on
  `/dev/shm/xhttp.sock`, has no TLS of its own, and must trust `X-Real-IP` in
  `sockopt.trustedXForwardedFor` to see client addresses. The provisioning
  playbooks in `ansible-playbooks` create it.
- **`grpc_pass`, not `proxy_pass`**: it streams the request body instead of
  buffering it first, so every xHTTP mode passes. `XHTTP_UPSTREAM=http`
  switches one node to `proxy_pass` with both buffers off, to compare the two on
  real traffic; any other value fails the render, so a typo stops the container
  instead of quietly running the default. Measured on one node in 2026-09
  (delay tests after 90 s of idle): `grpc` 1 failure in 20, `http` 6 in 20.

The location, line by line: [angie.conf → 4. Node virtual host](#4-node-virtual-host).

## Reality camouflage

The decoy site is bundled in the stack under `www/` and mounted read-only at
`/var/www/html`. Each node picks a different one via `CAMO_SITE`, so the fleet
is not fingerprintable by an identical page.

The value is assigned per node by the provisioning pipeline
(`dockhand-stack.yml` in the ansible-playbooks repository), seeded with the node
name so a re-run cannot pick differently and redeploy the stack. A value already
there — set by hand in Dockhand — is kept rather than overwritten. A stack
deployed outside that pipeline falls back to `converter`.

The mount uses `create_host_path: false` deliberately: a mistyped `CAMO_SITE`
then fails the deploy instead of silently mounting an empty directory — which
would serve a blank page and fingerprint the node just as effectively.

### Everything in the chosen directory is public

Angie serves `root /var/www/html` with no location restrictions, and the whole
decoy directory is mounted there. So **every file it contains is fetchable by
anyone probing the node** — there is no such thing as a private note inside
`www/<site>/`.

Five upstream `README.md` files shipped that way and were removed here:
`curl https://<node>/README.md` returned install instructions naming the
template repository, which identifies the page as camouflage more reliably than
an identical page across nodes ever would. `stack-guards.sh` check 3 now fails
any non-web file at that depth. Repo-level notes belong in `www/` itself (never
mounted) or in this README.

### Provenance

The decoy sites are third-party templates, vendored rather than fetched so that
a network failure cannot produce a half-rendered page — which is itself a
fingerprint. Known upstreams for this family of templates:

- [distillium/sni-templates](https://github.com/distillium/sni-templates)
- [proxyboy228/SNI-Templates](https://github.com/proxyboy228/SNI-Templates)
- `SmallPoppa/sni-templates` — cited by the removed READMEs, **now deleted**

**None of the three carries a licence**, so no permission can be established
and none is claimed here. This repository therefore ships no `LICENSE` of its
own: it cannot license what it does not own. The templates are credited, not
appropriated.

That an upstream vanished once is the reason to keep vendoring, and the reason a
fork under our own account is worth having as a stable reference.

## Notes

- The proxy listens on `unix:/dev/shm/nginx.sock`, the path xray/reality
  forwards to. **Do not rename it.**
- Delivery is polling (Dockhand scheduled sync), never inbound webhooks —
  admin surfaces stay private by default.
- Remaining hardening: the Cloudflare token is still zone-wide. Narrowing it
  via acme-dns delegation is tracked in issue #25.
