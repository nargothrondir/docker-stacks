# angie.conf — how it is built and why

🇬🇧 English · [🇷🇺 Русский](angie.ru.md)

[`angie.conf`](angie.conf) is kept free of commentary; this file carries it.
The sections below follow the numbered sections of the config. Deploying the
stack, its variables and the camouflage sites are in [README.md](README.md).

## How the file is used

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

## 1. Server identity

`server_tokens off` removes the version from the `Server` header and from error
pages. Replacing the name itself ("Angie") or dropping the header takes Angie
PRO — the open-source build accepts `on | off | build` only.

`server_names_hash_bucket_size 64` leaves room for long node names.

## 2. TLS

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

## 3. ACME client

Let's Encrypt, DNS-01 through the hook in [section 6](#6-acme-dns-01-hook), so no
inbound port is opened. State — the account key and the certificates — lives in
the `angie-acme` volume; see [README: Certificates](README.md#certificates),
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

## 4. Node virtual host

The server for this node's own name: its certificate (issued and renewed by
section 3), the `X-Robots-Tag` header that keeps the decoy out of search
engines, the decoy site itself ([README: Reality camouflage](README.md#reality-camouflage)),
and — when `XHTTP_PATH` is set — the xHTTP location
([README: xHTTP](README.md#xhttp)).

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

## 5. Catch-all virtual host

The catch-all server (`default_server`) refuses any handshake whose SNI is not
this node's name, before a certificate is shown. It also closes (`444`) a
request whose `Host` names no server — which can arrive after a handshake on
this node's name, and would otherwise be answered by the catch-all.

## 6. ACME DNS-01 hook

A loopback-only server the ACME client calls for each add and remove of the
`_acme-challenge` TXT record; the request goes on as headers to the `acme-hook`
service, which holds the DNS token. How the hook works and how to test it:
[README: The DNS-01 hook](README.md#the-dns-01-hook).

## Changing this file

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
