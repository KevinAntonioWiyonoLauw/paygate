# Worktrees

Parallel lines of work, each in its own checkout with its own ports and its own
database. They share one git object store, so branches are cheap and switching
never disturbs a running stack.

## Map

| Worktree | Branch | API | Web | Database |
|---|---|---|---|---|
| `G:\Project\paygate` | `main` | 8787 | 3000 | `paygate` |
| `G:\Project\paygate-wt\shopee-live-feed` | `feat/shopee-live-feed` | 8788 | 3001 | `paygate_live` |
| `G:\Project\paygate-wt\webhooks` | `feat/webhooks` | 8789 | 3002 | `paygate_hooks` |
| `G:\Project\paygate-wt\multi-merchant` | `feat/multi-merchant` | 8790 | 3003 | `paygate_multi` |

All four databases live in the one Postgres container on port 5433. Each
worktree has its own `.env` with `PAYGATE_ADDR`, `PAYGATE_PUBLIC_URL`, and
`POSTGRES_DB` set to the values above, so stacks never collide.

## Starting a stack

```powershell
# from any worktree root
.\ops\run.ps1
```

The gateway reads its own `.env`, so the port and database come from that file.
The dashboard is separate:

```powershell
cd web
$env:PAYGATE_BACKEND = "http://127.0.0.1:8788"   # match this worktree's API port
pnpm dev
```

## Creating another worktree

```powershell
git worktree add G:\Project\paygate-wt\<name> -b feat/<name> main

# then give it its own identity
$env = Get-Content G:\Project\paygate\.env -Raw
$env = $env -replace 'PAYGATE_ADDR=.*',        'PAYGATE_ADDR=127.0.0.1:8791'
$env = $env -replace 'PAYGATE_PUBLIC_URL=.*',  'PAYGATE_PUBLIC_URL=http://127.0.0.1:8791'
$env = $env -replace 'POSTGRES_DB=.*',         'POSTGRES_DB=paygate_<name>'
Set-Content G:\Project\paygate-wt\<name>\.env -Value $env -NoNewline

# create the database, then migrate
docker compose exec -T postgres psql -U paygate -d postgres -c "CREATE DATABASE paygate_<name> OWNER paygate;"
.\ops\run.ps1 -migrate-only
```

Pick the next free API port and web port; do not reuse 8787–8790 or 3000–3003.

## Removing one

```powershell
git worktree remove G:\Project\paygate-wt\<name>
git branch -d feat/<name>
docker compose exec -T postgres psql -U paygate -d postgres -c "DROP DATABASE paygate_<name>;"
```

`git worktree remove` refuses if the checkout is dirty. That is the guard
working — commit or discard first, or pass `--force` only when you mean it.

## Rules

- **Never check the same branch out twice.** git refuses, and if forced you get
  two working copies fighting over one ref.
- **Migrate before you start.** A fresh database has no tables; the gateway
  applies migrations on boot, but `-migrate-only` makes the failure obvious
  rather than buried in startup logs.
- **Bind a QRIS per database.** A new database has none, so order creation
  returns `409 not_configured` until you paste one into Settings.
- **Ports are the contract.** The `.env` in each worktree is the only thing
  keeping the stacks apart. Copy a `.env` between worktrees and they will fight
  over the same port and database.

## Backups

`G:\Project\paygate-wt\_scaffold-git-backup` holds the original `create-next-app`
repository that lived in `web/.git` before the project was consolidated into one
repo at the root. It is not a worktree; delete it once you are satisfied nothing
in that first scaffold commit is still wanted.
