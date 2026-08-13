# Stack: hawser

🇬🇧 English · [🇷🇺 Русский](README.ru.md)

The Hawser edge agent. This folder is the single source of the agent's compose
file, so it is edited here and nowhere else — but unlike every other stack in
this repository, **Dockhand does not deploy it.**

## ⚠️ Never create a From-Git stack from this folder

`roles/hawser` in
[ansible-playbooks](https://github.com/nargothrondir/ansible-playbooks) fetches
this file over HTTPS at a pinned commit and brings it up over SSH. That is the
only supported path.

This is not caution — it was measured on the lab node on 2026-08-13:

```
2bd614e0a5a9_hawser-hawser-1   Created        <- never started
hawser                          Exited (0)     <- stopped itself
PL-1                            Failed         <- no agent at all
```

Deploying this stack through Dockhand makes the agent run `compose up` on the
project that contains the agent. It stopped its own container, and the
replacement was left in `Created`, because the process that would have started it
was inside the container it had just stopped. `restart: unless-stopped` does not
help — it does not apply to a container that never ran.

Dockhand reported `Failed to up via Hawser: Connection error` 1.1 seconds after
sending the request, and the node was unmanaged until an Ansible run restored it.

The same reasoning rules out the first install: Dockhand reaches a node only
through Hawser, so a node without an agent cannot be sent one.

## How it actually gets to a node

1. `roles/hawser` fetches this file at a pinned commit — the repository is
   public, so no credential is involved
2. the role renders a `.env` beside it with the host's own values
3. `docker compose up -d` over SSH

Editing this file therefore reaches the fleet through the pin bump and an Ansible
run, not through a Dockhand poll.

## Environment (`.env`, written by the role)

| Variable | Required | Purpose |
|---|---|---|
| `TOKEN` | yes, **secret** | The agent token Dockhand issued for this environment |
| `DOCKHAND_SERVER_URL` | yes | The `wss://` edge endpoint, e.g. `wss://dockhand.example.com/api/hawser/connect` |
| `STACKS_DIR` | no (`/opt/hawser-stacks`) | Where the agent writes the stacks it deploys |
| `REQUEST_TIMEOUT` | no (`120`) | Seconds. The upstream default of 30 cancels a From-Git deploy mid-pull on a slow host |

`TOKEN` is a real credential and never appears in this file — it lives only in
the `.env`, which the role writes `0600` under `no_log`. A plausible-but-wrong
value strands the host: the agent starts, fails to authenticate, and only an
SSH-borne Ansible run brings it back. Ansible keeps the same value in OpenBao at
`infra/hawser`, keyed by inventory hostname.

## Why the mount paths are identical on both sides

`STACKS_DIR` is bound to the same path inside and outside the container. That is
load-bearing, not tidiness: the agent hands compose file paths to the Docker
daemon, which resolves them in the **host** namespace. A different path inside
the container would leave every stack it deploys pointing at nothing.

Both binds use the long form with `create_host_path: false`, so a wrong path
fails the deploy instead of silently mounting an empty directory — or, in the
socket's case, creating a directory named `docker.sock` and starting an agent
that is healthy and blind.
