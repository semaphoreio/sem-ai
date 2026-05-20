# sem-ai

Agent-first CLI for [Semaphore CI/CD](https://semaphore.io). Structured JSON output, self-discovery, composable commands, and an embedded MCP server — built for AI agents to drive the full CI/CD loop without a browser.

## Why sem-ai

- **JSON by default** — every command returns structured JSON. Agents parse it directly, humans use `--format table`
- **Self-discovery** — `sem-ai discover` returns a full capability map. Every command supports `--examples`
- **MCP server** — `sem-ai mcp` exposes all commands as native tools for Claude Code, Cursor, VS Code, and any MCP client
- **Compound commands** — `diagnose` composes workflow → pipeline → failed jobs → logs → parsed test results into a single call
- **Compatible** — shares `~/.sem.yaml` with the [legacy Semaphore CLI](https://github.com/semaphoreci/cli). Same tokens, same contexts

**Full reference**: [docs.semaphore.io/reference/sem-ai-cli](https://docs.semaphore.io/reference/sem-ai-cli)

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/semaphoreio/sem-ai/main/install.sh | sh
```

Installs the latest release automatically. Supports macOS and Linux on amd64 and arm64. The binary is placed at `$HOME/.local/bin/sem-ai` when that directory is on `$PATH`, or at `$HOME/.semaphore-ai/bin/sem-ai` otherwise (a `$PATH` hint is printed to stderr in that case).

Re-run the same command to update to the newest release (or to confirm you're already on the latest).

Want to inspect what runs first? `curl -fsSL https://raw.githubusercontent.com/semaphoreio/sem-ai/main/install.sh | less` before piping to `sh`.

### Advanced / manual install

For users who want to inspect-then-build, pin a specific commit, or work offline:

```sh
git clone https://github.com/semaphoreio/sem-ai.git
cd sem-ai
make install
```

Requires Go 1.25+.

## Updates

When running the sem-ai plugin in Claude Code or Codex, a `SessionStart` hook checks GitHub for new releases at most once every 6 hours and surfaces a one-line notice in chat when one is available:

```
sem-ai 0.4.1 is available (you have 0.3.0). Upgrade:
  curl -fsSL https://raw.githubusercontent.com/semaphoreio/sem-ai/main/install.sh | sh
```

To check from a shell at any time:

```shell
sem-ai version --check
```

To opt out (honored by both the plugin hook and manual CLI):

```shell
export SEM_AI_NO_UPDATE_CHECK=1
```

Upgrade by re-running the install script — it fast-paths if you're already on latest:

```shell
curl -fsSL https://raw.githubusercontent.com/semaphoreio/sem-ai/main/install.sh | sh
```

## Quick start

```shell
# Connect to your org (get token at https://me.semaphoreci.com/account)
sem-ai connect myorg.semaphoreci.com YOUR_API_TOKEN

# Check CI status
sem-ai status --project my-app --branch main

# Diagnose a failure — one command, full root cause
sem-ai diagnose --project my-app --branch main
```

## MCP server

Run sem-ai as a persistent MCP server. Starts once, handles all tool calls through the in-memory command tree.

```shell
sem-ai mcp
```

### Claude Code

Add to `.mcp.json` in your project:

```json
{
  "mcpServers": {
    "semaphore": {
      "command": "sem-ai",
      "args": ["mcp"]
    }
  }
}
```

All commands become native MCP tools (`project_list`, `diagnose`, `status`, `blast-radius`, etc). Long-running commands (`watch`, `promote-and-wait`) are excluded to prevent blocking.

## Agent skills

sem-ai ships its skill bundle as a Claude Code / Codex plugin. From inside your AI host, install with two slash commands:

```
/plugin marketplace add semaphoreio/sem-ai
/plugin install sem-ai@semaphoreio
```

The plugin drops every sem-ai skill (debug-pipeline, deploy, gha-to-semaphore, manage-infra, project-health, sem-ai-bootstrap, semaphore-blocks, semaphore-ci, semaphore-promotions, test-intelligence, testbox) into your host. Updates ride the marketplace — `/plugin update sem-ai@semaphoreio` whenever a new sem-ai release lands.

Skills follow the [Agent Skills](https://agentskills.io) standard and give agents context on when and how to use each sem-ai command without reading documentation.

## Commands

### Projects

| Command | Description |
|---------|-------------|
| `project list` | List all projects |
| `project show <name>` | Show project details |
| `project update <name>` | Update project settings |
| `project delete <name>` | Delete a project |

### Workflows

| Command | Description |
|---------|-------------|
| `workflow list --project <p>` | List workflows |
| `workflow show <id>` | Show workflow details |
| `workflow run --project <p>` | Rerun the latest workflow |
| `workflow rerun <id>` | Rerun a workflow |
| `workflow stop <id>` | Stop a workflow |

### Pipelines

| Command | Description |
|---------|-------------|
| `pipeline show <id>` | Show pipeline with blocks and jobs |
| `pipeline list --project <p>` | List pipelines |
| `pipeline stop <id>` | Stop a pipeline |
| `pipeline rebuild <id>` | Trigger partial rebuild (API call only) |
| `pipeline promote <id>` | Trigger a promotion (deploy) |
| `pipeline topology <id>` | Show block dependency graph |

### Jobs

| Command | Description |
|---------|-------------|
| `job list --states RUNNING` | List jobs by state |
| `job show <id>` | Show job details |
| `job log <id>` | Fetch structured job logs |
| `job stop <id>` | Stop a running job |

### Analytics

| Command | Description |
|---------|-------------|
| `analytics summary --project <p>` | Pass rate, duration, failures, deploys — all in one |
| `analytics duration --project <p>` | Duration trends (avg, p50, p95) with phase breakdown |
| `analytics failures --project <p>` | Block-level failure rates and failure reasons |
| `analytics queue --project <p>` | Queue wait time stats |
| `analytics deploys --project <p>` | Deploy frequency (per day / per week) |
| `analytics trend --project <p>` | Week-over-week trends for all key metrics |

All analytics commands accept `--project` (auto-detected from git), `--days`, `--branch`, and `--limit`. `analytics trend` uses `--weeks` instead of `--days`.

### Test intelligence

| Command | Description |
|---------|-------------|
| `test summary --pipeline <id>` | Parsed test results with file:line:message |
| `test report --pipeline <id>` | Detailed test report |
| `test flaky --project <p>` | Detect flaky tests across recent runs |

### Deployment targets

| Command | Description |
|---------|-------------|
| `deploy targets --project <p>` | List targets |
| `deploy show <id>` | Show target details |
| `deploy history <id>` | Deployment history |
| `deploy create <name>` | Create a target |
| `deploy activate <id>` | Activate a target |
| `deploy deactivate <id>` | Deactivate a target |
| `deploy delete <id>` | Delete a target |

### Secrets

| Command | Description |
|---------|-------------|
| `secret list` | List secrets (org or project level) |
| `secret show <name>` | Show secret details |
| `secret create <name>` | Create a secret |
| `secret update <name>` | Update a secret |
| `secret delete <name>` | Delete a secret |

### Notifications, tasks, agents

| Command | Description |
|---------|-------------|
| `notification list/show/create/delete` | Notification rules |
| `task list/show/create/run/delete` | Scheduled tasks |
| `agent types/show/list/delete` | Self-hosted agent management |

### Artifacts

| Command | Description |
|---------|-------------|
| `artifact list --scope jobs --id <id>` | List artifacts |
| `artifact get --scope jobs --id <id> --path <p>` | Download an artifact |

### Utility

| Command | Description |
|---------|-------------|
| `open` | Open workflow/pipeline in browser |
| `watch <workflow-id>` | Poll workflow until done, streaming status |
| `api-spec` | Fetch the Semaphore v2 OpenAPI spec |
| `version` | Print version information |
| `yaml validate --file <path>` | Validate pipeline YAML |
| `troubleshoot workflow/pipeline/job <id>` | Server-side diagnostics |

## Compound commands

These compose multiple API calls into a single operation.

| Command | What it does |
|---------|-------------|
| `status` | CI status for a branch — pipeline state, block results |
| `diagnose` | Full failure diagnosis — logs, test results, root cause |
| `health` | Project health — pass rates, trends, deploy status, verdict |
| `analytics summary` | All-in-one analytics overview for a project over a time window |
| `analytics trend` | Week-over-week trends — pass rate, duration, queue, failures |
| `critical-path <id>` | Longest dependency chain (bottleneck) |
| `blast-radius <id>` | Root failures vs cascading cancellations |
| `rerun-failed <id>` | Partial rebuild of failed blocks only |
| `promote-and-wait <id>` | Promote and block until promoted pipeline finishes |

## Testbox

Run commands in a real Semaphore CI environment before pushing. Creates a warm VM, syncs your local code, executes commands via SSH.

```shell
# Start a testbox
sem-ai testbox warmup --project my-app

# Run tests in real CI env
sem-ai testbox run --id <id> "go test ./..."

# Interactive SSH
sem-ai testbox ssh --id <id>

# Stop when done
sem-ai testbox stop --id <id>
```

## Output

All commands output JSON by default:

```shell
sem-ai status --project my-app          # JSON
sem-ai status --project my-app -f table # human-readable table
sem-ai status --project my-app -f yaml  # YAML
```

Errors are structured JSON on stderr:

```json
{"error": true, "code": "not_found", "message": "project not found", "status": 404}
```

## Configuration

sem-ai uses `~/.sem.yaml` — the same config file as the [legacy Semaphore CLI](https://github.com/semaphoreci/cli). If you already have `sem` configured, sem-ai works immediately.

```shell
sem-ai connect myorg.semaphoreci.com YOUR_TOKEN  # add/update context
sem-ai context list                               # list all orgs
sem-ai context show                               # show active org
```

## Development

```shell
# Build
make build

# Install locally
make install

# Run tests
make test

# Format and vet
make fmt
make vet
```

### Project structure

```
cmd/           Command implementations (one file per resource)
pkg/client/    HTTP client with retry, pagination, versioned API support
pkg/config/    Configuration loading from ~/.sem.yaml
pkg/output/    JSON/table/YAML output engine
pkg/testparse/ Test result parsers (Go, pytest, rspec, minitest, jest, ExUnit, JUnit)
pkg/gitutil/   Git remote/branch detection
.agents/       Agent skill definitions
```

### Adding a command

1. Create `cmd/<resource>.go`
2. Define a `cobra.Command` with `RunE`, `Short`, `Example`
3. Register it in `init()` with `rootCmd.AddCommand()` or as a subcommand
4. The command automatically appears in `discover`, MCP tools, and `--help`

