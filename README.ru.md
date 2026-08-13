# docker-stacks

[![Validate](https://github.com/nargothrondir/docker-stacks/actions/workflows/validate.yml/badge.svg)](https://github.com/nargothrondir/docker-stacks/actions/workflows/validate.yml)

[🇬🇧 English](README.md) · 🇷🇺 Русский

Docker Compose-стеки, разворачиваемые через **Dockhand → From Git**. Этот
репозиторий — **единственный источник правды** для app-стеков: Dockhand
клонирует каталог стека (compose + соседние конфиги) и деплоит его в целевое
окружение через edge-агент Hawser.

Подготовка хостов/ОС живёт в
[ansible-playbooks](https://github.com/nargothrondir/ansible-playbooks); этот
репозиторий отвечает только за то, что работает **в контейнерах**.

## Стеки

| Стек | Назначение | Окружения | Секреты (Dockhand env) |
|------|------------|-----------|------------------------|
| [remnanode](remnanode/) | Нода Remnawave + reality-fallback прокси (Angie) | fi / kz / ru | `SECRET_KEY` |
| [semaphore](semaphore/) | Semaphore (веб-UI Ansible) — control plane | panel | `SEMAPHORE_ADMIN_PASSWORD`, `SEMAPHORE_ACCESS_KEY_ENCRYPTION` |
| [test-fromgit](test-fromgit/) | Одноразовый тест From-Git конвейера (временный) | test | — |

## Правила

- **Никаких секретов в git.** Значения задаются как **secret env** в Dockhand
  для стека/окружения; compose ссылается на них через `${VAR}`.
- **Один каталог = один стек.** Всё нужное стеку (compose, конфиг прокси) лежит
  в его каталоге — Dockhand доставляет каталог на хост целиком.
- **Версии образов пиновать.** Плавающие теги делают обновления неотслеживаемыми.
- Host-зависимости (например, сертификаты certbot в `/etc/letsencrypt`)
  документируются в README каждого стека.

## Спецификация агента

[`CLAUDE.md`](CLAUDE.md) содержит правила работы с AI-ассистентом здесь. Он
намеренно тонкий: только то, что нельзя вывести самому (мёрж бампа версии
**и есть** развёртывание; образ `-templated` рендерит через gomplate, а не
envsubst; какие тома хранят невосстановимое состояние), а общие правила берутся
из [основной спецификации](https://github.com/nargothrondir/ansible-playbooks/blob/main/CLAUDE.md).

## CI

Каждый push проверяет **все** стеки автоматически (новые каталоги подхватываются
без правок workflow): `angie -t` для каждого `*/angie.conf`,
`docker compose config` для каждого `*/docker-compose.yml`, gitleaks и
`stack-guards.sh` (пины образов, парные README стеков).
Еженедельный scheduled-прогон ловит «гниение» без коммитов.
