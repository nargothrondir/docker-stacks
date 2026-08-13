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
