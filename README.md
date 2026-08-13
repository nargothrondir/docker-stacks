# docker-stacks

[![Validate](https://github.com/nargothrondir/docker-stacks/actions/workflows/validate.yml/badge.svg)](https://github.com/nargothrondir/docker-stacks/actions/workflows/validate.yml)

🇬🇧 English · [🇷🇺 Русский](README.ru.md)

Docker Compose stacks deployed via **Dockhand → From Git**. This repository is
the **single source of truth** for app stacks: Dockhand clones a stack's folder
(compose + sibling config files) and deploys it to the target environment
through the Hawser edge agent.

Host/OS provisioning lives in
[ansible-playbooks](https://github.com/nargothrondir/ansible-playbooks); this
repo only owns what runs **in containers**.

**One exception to "deployed via Dockhand": [`hawser/`](hawser/).** It is the
agent Dockhand delivers everything else *through*, so Dockhand cannot deliver it
— deploying it that way makes the agent stop its own container mid-command and
leaves the node unmanaged (measured, see its README). Ansible fetches that one
from here and deploys it over SSH. The folder is still the single source of its
compose; only the delivery differs.

## Stacks

| Stack | Purpose | Environments | Secrets (Dockhand env) |
|-------|---------|--------------|------------------------|
| [hawser](hawser/) | The Hawser edge agent — ⚠️ deployed by **Ansible**, never by Dockhand; read its README before touching it | every node, via `roles/hawser` | — (`TOKEN` lives in a role-written `.env`) |
| [remnanode](remnanode/) | Remnawave node + reality-fallback proxy (Angie) | fi / kz / ru | `SECRET_KEY` |
| [semaphore](semaphore/) | Semaphore (Ansible web UI) — control plane | panel | `SEMAPHORE_ADMIN_PASSWORD`, `SEMAPHORE_ACCESS_KEY_ENCRYPTION` |
| [test-fromgit](test-fromgit/) | Throwaway From-Git pipeline test (temporary) | test | — |

## Rules

- **No secrets in git.** Secret values are provided as Dockhand **secret env
  vars** per stack/environment; compose references them as `${VAR}`.
- **One folder = one stack.** Everything the stack needs (compose, proxy config)
  lives in its folder — Dockhand ships the whole folder to the host.
- **Pin image versions.** Floating tags make updates untraceable.
- Host-side dependencies (e.g. certbot certificates under `/etc/letsencrypt`)
  are documented in each stack's README.

## Agent specification

[`CLAUDE.md`](CLAUDE.md) holds the rules for AI-assisted work here. It is thin
on purpose: only what cannot be inferred (merging a version bump **is** a
deployment; the `-templated` image renders with gomplate, not envsubst; which
volumes hold state that cannot be regenerated), deferring the shared rules to
the [main spec](https://github.com/nargothrondir/ansible-playbooks/blob/main/CLAUDE.md).

## CI

Every push validates **all** stacks automatically (new folders are picked up
with no workflow edits): `angie -t` for every `*/angie.conf`,
`docker compose config` for every `*/docker-compose.yml`, gitleaks secret
scanning, and `stack-guards.sh` (image pins, bilingual stack READMEs).
A weekly scheduled run catches bit-rot.
