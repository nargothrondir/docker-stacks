# Stack: hawser

🇬🇧 English · [🇷🇺 Русский](README.ru.md)

The Hawser edge agent, deployed as a Dockhand From-Git stack so that its compose
file has one home and changes reach the fleet by polling instead of by hand.

## ⚠️ This stack is the delivery mechanism for every other stack

Read this before touching it. Hawser is what Dockhand talks to; everything else
here arrives *through* it. Two consequences that do not apply to any other
folder in this repository:

**A bad merge here breaks management of every host at once,** at the next poll,
and cannot be fixed from Dockhand — the fix would have to travel through the
agent it just broke. Recovery is an Ansible run per host, over SSH:

```bash
ansible-playbook playbooks/hawser.yml --limit <host>
```

**A redeploy of this stack is the agent recreating its own container.** The
instruction arrives over the connection the recreation drops. The container
should survive — the Docker daemon performs the recreation, not the client that
requested it — but whether the deploy is ever reported *complete* is something
the agent cannot answer about itself. That behaviour is being established on the
lab node; until it is, do not deploy this stack to a production environment.

## The first install cannot come from here

Dockhand reaches a node only through Hawser, so a node with no agent cannot be
sent one. `roles/hawser` in
[ansible-playbooks](https://github.com/nargothrondir/ansible-playbooks)
bootstraps the agent on a bare host and remains the disaster-recovery path,
exactly as `roles/semaphore` does for the `semaphore` stack.

So this stack does not replace the role. It takes over the *lifecycle* after the
role has done the *bootstrap*.

## Deploy

Dockhand → Stacks → **From Git**, path `hawser`, into the target host's
environment.

The stack deliberately declares **no `container_name`**. The bootstrap project at
`/opt/hawser` claims the name `hawser`, and an explicit `container_name` collides
across compose projects — Docker refuses the second one outright. Letting compose
derive the name is what allows this stack to deploy while the bootstrap agent is
still running, which is the only order the handover can happen in.

## Environment

| Variable | Required | Purpose |
|---|---|---|
| `TOKEN` | yes, **secret** | The agent token Dockhand issued for this environment |
| `DOCKHAND_SERVER_URL` | yes | The `wss://` edge endpoint, e.g. `wss://dockhand.example.com/api/hawser/connect` |
| `STACKS_DIR` | no (`/opt/hawser-stacks`) | Where the agent writes the stacks it deploys |
| `REQUEST_TIMEOUT` | no (`120`) | Seconds. The upstream default of 30 cancels a From-Git deploy mid-pull on a slow host |

`TOKEN` must be the token Dockhand actually issued for **this** environment. A
value that merely looks valid strands the host: the agent starts, fails to
authenticate, and only an SSH-borne Ansible run brings it back. Ansible stores
the same value in OpenBao at `infra/hawser`, keyed by inventory hostname.

## Why the mount paths are identical on both sides

`STACKS_DIR` is bound to the same path inside and outside the container. That is
load-bearing, not tidiness: the agent hands compose file paths to the Docker
daemon, which resolves them in the **host** namespace. A different path inside
the container would leave every stack it deploys pointing at nothing.

Both binds use the long form with `create_host_path: false`, so a wrong path
fails the deploy instead of silently mounting an empty directory — or, in the
socket's case, creating a directory named `docker.sock` and starting an agent
that is healthy and blind.
