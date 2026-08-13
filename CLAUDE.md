# AI Agent Guidelines — docker-stacks

*Version 1.0*
*Companion to the spec in [ansible-playbooks](https://github.com/nargothrondir/ansible-playbooks/blob/main/CLAUDE.md)*

---

**What this repo is:** the **Workload** half of the platform. Application
containers live here as Dockhand From-Git stacks; the hosts they run on are
provisioned by the `ansible-playbooks` repository. That split is defined in
§12 of the main spec, and the test that decides where something belongs is:

> **If Dockhand were down, must this container still come up on its own?**
> **Yes → Platform (Ansible). No → Workload (here).**

**How to read this:** [`README.md`](README.md) already documents the stacks,
the no-secrets-in-git rule, the one-folder-one-stack layout and CI. This file
adds only what an agent cannot infer and would get wrong.

**Shared rules that apply here unchanged** — read them in the main spec rather
than a copy: safety priority and the confirmation gate (§2, §4), English
artifacts and paired bilingual READMEs (§1), Conventional Commits with a
mandatory scope (§9 — here the scope is the stack name, e.g.
`feat(remnanode): …`), and diff discipline (§4).

**Issues live in the private archive repository, not here.** This repository
became public from a history-free snapshot; the backlog stayed behind because
its issues carry the real domain and the fleet's node names. So `gh issue`
needs `--repo <owner>/docker-stacks-archive`, and a new issue is opened THERE —
including issues about code that lives here. A `#N` in a comment is provenance
for a decision, not a link a reader can follow. The same arrangement, and the
same reason, as `ansible-playbooks`.

---

## 1. Merging a version bump IS a deployment

Dockhand polls this repository and redeploys when a stack's folder changes.
**The pinned tag in git is the version running in production** — there is no
separate deploy step to catch a mistake.

Consequences an agent must act on:

- A Renovate PR is not a chore. A major bump is a production upgrade and gets
  the same scrutiny as a code change: read the upstream changelog, check for
  breaking changes, and say so in the review.
- Never merge a major bump of `remnawave/node`, `angie` or `semaphore` without
  a decision recorded in an issue.
- Prove a risky change on the lab node first, then **one production node at a
  time** — never the fleet at once.

## 2. Delivery is polling, never inbound webhooks

Admin surfaces are private-by-default behind the mesh, so an inbound webhook
from GitHub is not an option that merely has not been built — it is
architecturally excluded. Do not propose one.

## 3. Angie `-templated` renders with gomplate, not envsubst

The `-templated` image runs **gomplate** over `/etc/angie/templates/` into
`/etc/angie/`. Template syntax is `{{ .Env.VARIABLE }}` — not `${VARIABLE}`,
not `$VARIABLE`.

This is why Angie's own `$variables` survive untouched: gomplate's `{{ }}`
does not collide with them. Getting this wrong produces a config that renders
to garbage but still passes a casual read.

Config files are mounted into the **templates input directory**, not their
final path.

## 4. Fail loudly at deploy time, never silently

Established conventions in the compose files — keep them and extend them:

- Required env vars use `${VAR:?message}` so a missing value aborts the deploy
  instead of rendering an empty string into a config.
- Bind mounts of repo content use `create_host_path: false`, so a mistyped
  path errors instead of mounting an empty directory. `CAMO_SITE` is the
  worked example: a silent empty mount would serve a blank page and
  fingerprint the node — worse than a failed deploy.

## 5. Volumes carry state that cannot be regenerated

- `angie-acme` holds the ACME **account key and issued certificates**. Do not
  clear it on a live node: the config serves the certificate *from* the
  volume, so an empty volume means no TLS until issuance completes — and each
  re-issue burns a duplicate-certificate slot.
- Semaphore's volumes are declared `external` and point at the ones created by
  the original Ansible deployment. Renaming them, or letting compose create
  fresh ones, starts an empty control plane while the real data sits orphaned.

## 6. Secrets

Values are Dockhand **secret env vars**, referenced as `${VAR}` in compose.
They never enter git, and they never enter chat — not for debugging, not as an
example. If a secret does appear in conversation, treat it as compromised and
say so.

## 7. What CI enforces

`.github/workflows/validate.yml` runs on every push, over **all** stacks:

- `angie -t` for every `*/angie.conf` (rendered through gomplate first)
- `docker compose config` for every `*/docker-compose.yml`
- gitleaks secret scanning — deliberately unfiltered, because secrets land in
  documentation as often as in code
- `.github/scripts/stack-guards.sh` — every image pinned to a tag or digest
  (never `:latest`, never bare), every stack documented in both languages

A rule that CI can check belongs in CI, not in this document (main spec §13).
When you add such a check here, delete the prose it replaces.
