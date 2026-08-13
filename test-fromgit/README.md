# Stack: test-fromgit — disposable

🇬🇧 English · [🇷🇺 Русский](README.ru.md)

A throwaway stack that existed to prove the From-Git pipeline reaches a
**remote** host: git → Dockhand → Hawser → the node's Docker (issue #12).

Plain nginx, no published ports, no volumes — safe to deploy anywhere and
delete afterwards.

## Status: kept only until the lab node is torn down

The pipeline it validated is proven and in production use, so this stack has
no remaining purpose. It is slated for removal together with the lab node
(teardown checklist: `ansible-playbooks` issue #47).

**Before deleting the folder, remove the corresponding stack in Dockhand** —
otherwise the environment keeps a stack whose source no longer exists.
