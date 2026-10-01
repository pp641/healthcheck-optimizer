# Tidyfleet

A disk cleaner for developers that doubles as a privacy-respecting laptop health dashboard for small companies.

- **Personal mode (free):** the `tidyfleet` agent finds regenerable developer clutter, such as stale `node_modules`, Rust `target/`, Xcode DerivedData, package caches, and Docker images. It shows a dry-run preview and moves items to the Trash.
- **Company mode:** once a laptop is enrolled with an organization code, the same agent sends health totals (disk, memory, battery, OS, encryption, firewall) to a dashboard.

**File-level data never leaves the laptop.** The company sees totals, never file names. The server decodes every snapshot into a fixed struct before storing it, so a field it doesn't know about (a path, say) is dropped, never stored.

```
agent/       Go agent + CLI (scanner, cleaner, disk tree, health collector, reporter, local UI server)
cleaner-ui/  Next.js cleaner app, exported as static files and embedded in the agent binary
server/      Go API: enrollment, snapshot ingest, fleet/device APIs, alert rules, Slack
dashboard/   Next.js dashboard: fleet overview, device history, alerts, settings
packaging/   macOS LaunchAgent + install script
```

## Quick start

**One command (macOS):** `make try` starts the dashboard in Docker, creates an organization, enrolls this Mac, prints the dashboard login, and opens the cleaner app. Re-running it is safe.

**Just the cleaner:** `make cleaner` builds and opens the cleaner app. It includes:

- a disk-usage tree of your project folders, largest first
- badges on every folder a rule matches
- checkboxes on the folders that are safe to clean
- a dry-run preview, then Clean
- **Big view:** opens Tree or Tiles in its own tab, filling the window, with a full-screen button
- the cleanup log, settings, and a health & sharing page
- **hand-picked folders:** select any folder (⌘/Shift-click, or checkboxes in the list), or drag it onto another folder, to **Move to…** or **Move to Trash…**. These are manual actions, separate from safe cleanup:
  - they always go to the Trash, never a permanent delete
  - there's a preview that warns about git repos, uncommitted work, and recent changes
  - scan locations, home and system folders, and never-touch folders are refused
  - moves stay on one disk and can be undone
  - everything is logged

It runs on 127.0.0.1 only and needs the private link it opens. Clean re-scans first, so every safety check runs again on current data.

### Step by step

You need Docker and Node 20+. Go does not need to be installed: the Makefile builds and tests Go inside the `golang:1.24` image.

```sh
make up                                             # Postgres + API (:8080) + dashboard (:3000)
make create-org NAME="Alcyon Labs" EMAIL=you@alcyon.test
```

`create-org` prints a generated admin password (shown once) and the enrollment code. Sign in at http://localhost:3000.

Build the agent and enroll this Mac:

```sh
make agent-darwin                                   # bin/tidyfleet-darwin-arm64 and -amd64
bin/tidyfleet-darwin-arm64 enroll http://localhost:8080 TF-XXXXX-XXXXX
```

If something else already uses port 8080 or 3000, use other ports: `TIDYFLEET_API_PORT=8088 TIDYFLEET_DASHBOARD_PORT=3001 make up`. On macOS, `localhost` may resolve to a different process than `127.0.0.1`. If enrollment reaches the wrong service, use `http://127.0.0.1:<port>`.

## The agent

```
tidyfleet ui [--port N] [--no-open]    the cleaner app in your browser (local only)
tidyfleet scan [--all] [--json]         find artifacts (read-only); --all shows skipped items and why
tidyfleet clean [--dry-run] [--yes]     preview, confirm, then clean; also --rule, --only <ids>, --min-mb
tidyfleet log                           local cleanup log (what was removed, where it went)
tidyfleet config show | set <k> <v> | path
tidyfleet rules                         built-in + custom rules, and whether each is active here
tidyfleet health [--updates] [--json]   this laptop's metrics, exactly as they would be sent
tidyfleet enroll <server-url> <code>    join an organization (shows what is shared, asks first)
tidyfleet report                        send a snapshot now
tidyfleet shared                        the exact last snapshot the organization received
tidyfleet leave [--force]               stop reporting; the server deletes this device's history
tidyfleet daemon                        background loop (see below)
```

### Safety rules (enforced in code and covered by tests)

- Only folders matched by a rule are eligible. A rule matches a folder name next to a project marker (such as `node_modules` next to `package.json`), a known cache path, or a tool probe (Docker, `simctl`).
- A folder is skipped if git tracks any file in it, if the rule requires gitignored output and the folder isn't ignored, if the repo has uncommitted changes (unless `clean_dirty_repos` is on), or if the project was active within `stale_days` (default 60).
- Guards are checked again right before deletion: absolute path, not a symlink, not a git repo, not a scan root, inside the scan locations or at the rule's known path, and not excluded.
- Items go to the Trash by default. Permanent delete is opt-in and needs you to type `delete`.
- Where a tool has its own clean command, the agent runs it instead of deleting files (`docker image prune`, `npm cache clean`, and so on).
- Every action is appended to `cleanup-log.jsonl`.

`agent/internal/cleaner/safety_test.go` builds real throwaway git repos and checks each of these cases end to end.

### Settings

`tidyfleet config set <key> <value>`. Lists are comma-separated.

| Key | Default | |
|---|---|---|
| `scan_roots` | existing `~/Projects`, `~/code`, `~/Developer`, … | folders searched for projects |
| `exclude` | none | never scanned or cleaned |
| `disabled_rules` | none | rule ids to switch off |
| `stale_days` | 60 | project inactivity before its build output is eligible |
| `delete_mode` | `trash` | or `permanent` |
| `always_preview` | true | with `false`, `clean --yes` skips the item list |
| `clean_dirty_repos` | false | still only gitignored/regenerable folders |
| `schedule` | `manual` | `weekly` = background scan + "you can reclaim X GB" notification; never auto-deletes |
| `ai_mode` | `off` | `local` / `cloud`; locked to `off` if the organization disallows AI |

Only the employee controls these. The organization controls only the reporting interval and whether AI is allowed. `config show` marks those as managed.

Custom rules go in `<config dir>/rules/*.yaml`, and a rule with the same `id` as a built-in one replaces it. The config dir is `~/Library/Application Support/Tidyfleet` on macOS, or `$TIDYFLEET_HOME` if set.

### Background agent (macOS)

```sh
make install-agent            # installs ~/.local/bin/tidyfleet + ~/Library/LaunchAgents/com.tidyfleet.agent.plist
packaging/macos/install.sh --uninstall
```

The daemon runs as the logged-in user at low priority. When enrolled, it reports every policy interval (default 60 min) and retries queued snapshots every 5 minutes. It refreshes the scan total once a day. With `schedule=weekly`, it sends a reminder notification. It never deletes anything. Logs go to `~/Library/Logs/Tidyfleet/agent.log`.

**Offline queue:** snapshots are written to `<config dir>/queue/` and sent oldest first, in batches of 50. The queue holds up to 1000 snapshots, about six weeks at the default interval. Retries are safe because the server ignores a snapshot it already has for the same device and timestamp.

## The server

`tidyfleet-server [serve | migrate | create-org -name N -admin-email E [-admin-password P]]`

| Env | Default | |
|---|---|---|
| `DATABASE_URL` | required | Postgres 13+ |
| `LISTEN_ADDR` | `:8080` | |
| `TIDYFLEET_LATEST_OS` | none | e.g. `macos=26,windows=10`; otherwise the newest version seen in the fleet |
| `TIDYFLEET_RETENTION_DAYS` | 365 | snapshot history kept |
| `TIDYFLEET_TRUST_PROXY` | off | `1` to use `X-Forwarded-For` for rate limiting |

Migrations are embedded and apply on start. An advisory lock makes concurrent starts safe.

**API.** The agent uses `POST /api/v1/enroll`, `/api/v1/snapshots` and `/api/v1/leave`, authenticated with a device bearer token. The dashboard uses `/api/v1/auth/*`, `/fleet`, `/devices/{id}[/snapshots]`, `/alerts`, `/alert-rules`, and `/org`. Tokens are random 256-bit values, and only their SHA-256 hashes are stored. Passwords are hashed with bcrypt. Enrollment and login are rate-limited on failed attempts only, so an office behind one NAT address can enroll together.

**Alerts** are checked on every snapshot and in a 5-minute sweep, which also catches laptops that stop reporting. New orgs get these rules: disk above 90%, OS 2+ major versions behind, battery below 70%, encryption off, and no report for 7 days. Slack gets a message when an alert opens or resolves. Webhook URLs must start with `https://hooks.slack.com/`, so the server can't be used to reach other hosts. "Versions behind" handles Apple's jump from macOS 15 to 26.

## Development

```sh
make test          # agent tests + server tests against a throwaway Postgres container
make vet           # go vet for linux, darwin and windows
make agent-all     # macOS (arm64/amd64), Windows and Linux binaries
cd dashboard && npm install && API_URL=http://localhost:8080 npm run dev
make cleaner-ui    # rebuild the cleaner app and embed it (then make agent-darwin)
```

## Not built yet

- **Proposal items still to do:** Windows MSI and service, a native desktop window (the cleaner app runs in the browser for now; Wails can wrap the same Next.js export later), the treemap view, the AI assistant, email alerts and the weekly summary, billing.
- **Agent:** the device token is stored in a 0600 file in the config dir, not the Keychain.
- **Server:** there is no UI for inviting more users. The `users.role` column supports read-only `viewer` accounts, but there is nothing yet to create them. `device_snapshots` is a plain table for now; partition it monthly once volume calls for it.
- **Windows OS versions:** Windows 10 and 11 both report major version 10, so "versions behind" is not meaningful on Windows yet.
- **Before a commercial launch:** check the employer's outside-activities policy (proposal §14).
# healthcheck-optimizer
