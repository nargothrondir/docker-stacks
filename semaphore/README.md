# Stack: semaphore

🇬🇧 English · [🇷🇺 Русский](README.ru.md)

Semaphore (Ansible web UI) for the control host, deployed as a Dockhand
From-Git stack.

## Deploy

Dockhand → Stacks → **From Git**, path `semaphore`, into the control host's
environment. Listens on `127.0.0.1:3030` and is published by the host's
reverse proxy; it is never exposed directly.

## Secrets (Dockhand secret env vars)

| Variable | Purpose |
|---|---|
| `SEMAPHORE_ADMIN_PASSWORD` | initial admin password |
| `SEMAPHORE_ACCESS_KEY_ENCRYPTION` | base64, 32 bytes — encrypts the Key Store |

Losing `SEMAPHORE_ACCESS_KEY_ENCRYPTION` makes every stored SSH key and vault
password unreadable. It belongs in a password manager, not only in Dockhand.

## ⚠️ External volumes — do not rename

The three volumes are declared `external` and point at the volumes created by
the original Ansible deployment (compose project `semaphore`), so the
migration to From-Git kept all projects, Key Store entries and task history.

Renaming them, or letting compose create fresh ones, silently starts an empty
Semaphore while the real data sits in orphaned volumes.

## Self-update caveat

Dockhand recreates this container over the Docker socket, which is how the
control plane can update itself. Two consequences worth knowing:

- Upgrading the **Docker engine** on this host restarts the daemon and kills
  any Ansible run in flight — including the run doing the upgrade. Do not let
  the scheduled fleet upgrade touch the control host unattended.
- The Ansible role `roles/semaphore` in the `ansible-playbooks` repository
  remains the disaster-recovery path: it can rebuild this from a bare host.

## OpenBao network

The stack joins the external Docker network `openbao-net`, created by
`roles/openbao` in `ansible-playbooks`, and reaches the secret store as
`http://openbao:8200`.

Semaphore reads secrets **on behalf of** playbooks, so it — not the target host
— is the client that needs the API. OpenBao publishes only on the panel's
loopback, which a container cannot reach; publishing on the bridge gateway
instead would expose the secret store to every container on the host.

The network must exist before this stack deploys. Apply `roles/openbao` first.
