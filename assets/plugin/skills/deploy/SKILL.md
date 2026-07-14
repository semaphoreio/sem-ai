---
name: deploy
description: Deploy via Semaphore promotions. Manage deployment targets, promote pipelines, deploy-and-wait.
user-invocable: false
---

# Deployments and Promotions

## See available promotions
```bash
sem-ai pipeline promote <pipeline-id>
# Returns list of available promotion targets (no --target = list mode)
```

## Deploy (promote)

```bash
# Dry run (safe — does NOT execute)
sem-ai pipeline promote <id> --target "Staging Deploy"

# Execute
sem-ai pipeline promote <id> --target "Staging Deploy" --confirm

# Execute and wait for promoted pipeline to finish
sem-ai promote-and-wait <id> --target "Staging Deploy" --confirm

# Override conditions (deploy despite failures)
sem-ai pipeline promote <id> --target "Staging" --confirm --override

# With parameters (each --param becomes a promotion env var)
sem-ai pipeline promote <id> --target "Production" --confirm --param version=1.2.3
sem-ai pipeline promote <id> --target "Production Deploy" --confirm --param SERVICE=web
```

## Deployment targets — read

```bash
sem-ai deploy targets --project my-app    # list
sem-ai deploy show <target-id>            # details
sem-ai deploy history <target-id>         # history
```

## Deployment targets — lifecycle

```bash
sem-ai deploy activate <target-id>        # enable
sem-ai deploy deactivate <target-id>      # cordon (disable)
sem-ai deploy delete <target-id>          # remove
```

## Deployment targets — create

`sem-ai deploy create <name>` builds the target's restriction surface from flags. **If a rule type (BRANCH / TAG / PR) has no flag, that type is implicitly blocked** — this is the right safe default for tag-only release targets.

### Tag-only release target (auto-promote only)
```bash
sem-ai deploy create release \
  --project my-app \
  --description "Auto-promoted on v* tag" \
  --tag-regex '^v[0-9]+\.[0-9]+\.[0-9]+$' \
  --subject-auto \
  --env-var GITHUB_TOKEN=<personal-access-token>
```
Result: only auto-promotion (from `auto_promote.when`) on tags matching the regex can trigger this. Manual clicks blocked. Branch/PR pipelines cannot promote here.

### Main-branch-only snapshot target
```bash
sem-ai deploy create snapshot \
  --project my-app \
  --branch-exact main \
  --subject-any
```
Result: any team member can promote, but only on the `main` branch.

### Object rules — pick per type

| Flag | Effect |
|------|--------|
| `--tag-regex <pattern>` | Allow tags matching Perl-compatible regex |
| `--tag-exact <name>` | Allow exact tag name only |
| `--allow-all-tags` | Allow promotion from any tag |
| `--branch-regex <pattern>` | Allow branches matching regex |
| `--branch-exact <name>` | Allow exact branch name only |
| `--allow-all-branches` | Allow promotion from any branch |
| `--allow-prs` | Allow promotion from pull requests |

You can pass multiple type flags in one call (e.g. `--branch-exact main --tag-regex '^v[0-9].*'` to allow main-branch promotions AND v-tag promotions).

### Subject rules — who can trigger

| Flag | Effect |
|------|--------|
| `--subject-any` | Anyone with project access |
| `--subject-user <uuid-or-git_login>` | Specific user (repeatable; UUID auto-detected vs git_login) |
| `--subject-role <role>` | Members of a role like `Admin` or `Contributor` (repeatable) |
| `--subject-auto` | Auto-promotion conditions only — blocks manual clicks |

If no subject flag is passed, the API default applies (typically `ANY`).

### Target-bound secrets

```bash
--env-var NAME=VALUE                  # repeatable
--file /etc/conf=/local/source.txt    # repeatable; base64-encodes content
```

Secrets bound to a deployment target are only visible to pipelines triggered THROUGH that target — more restrictive than project-level secrets. Prefer these for release credentials (e.g. `GITHUB_TOKEN` for goreleaser, deploy keys, registry creds).

### Deployment history filters (bookmarks)

```bash
--bookmark1 staging   --bookmark2 us-east-1   --bookmark3 v1.2
```
Pure metadata for filtering the deployment history page on the Semaphore web UI. Does NOT restrict who/what can trigger — that's `--subject-*` / `--branch-*` / `--tag-*`.

## Deployment targets — update

`sem-ai deploy update <target-id>` PATCHes the target. Only flags you pass are sent; everything else is preserved server-side.

```bash
# Tighten the release tag pattern
sem-ai deploy update <id> --tag-regex '^v[0-9]+\.[0-9]+\.[0-9]+$'

# Rotate a target-bound secret
sem-ai deploy update <id> --env-var GITHUB_TOKEN=<new-pat>

# Rename
sem-ai deploy update <id> --name release-v2

# Replace subject rules with a more restrictive set
sem-ai deploy update <id> --subject-role Admin --subject-auto
```

Note: list/array fields (`object_rules`, `subject_rules`, `env_vars`, `files`) are **replaced** by what you send — not merged. To preserve existing rules, re-pass them alongside the new ones, OR fetch the current state via `deploy show` first.

## Pipeline YAML reference

In the pipeline's `promotions:` block, reference the target by name:

```yaml
promotions:
  - name: Publish GitHub Release
    pipeline_file: release.yml
    auto_promote:
      when: "tag =~ '^v.*' AND result = 'passed'"
    deployment_target: release
```

The `deployment_target:` line enforces gating on BOTH auto and manual triggers, unlike `auto_promote.when` which only gates auto-firing.

## Full deploy workflow
```bash
# 1. Verify tests pass
sem-ai test summary --pipeline <id>

# 2. Deploy to staging and wait
sem-ai promote-and-wait <id> --target "Staging" --confirm

# 3. Check staging result
# (promoted pipeline ID is in the output above)

# 4. Deploy to production
sem-ai promote-and-wait <id> --target "Production" --confirm
```

## Safety
- Without `--confirm`: dry run only.
- Always verify tests before promoting.
- `--override` bypasses conditions — confirm with user first.
- Creating a deployment target without any `--subject-*` flag may default to `ANY` (anyone can trigger) — surface this when scaffolding release targets and prefer `--subject-auto` for tag-only release flows.
- `deploy update` REPLACES list fields. To merge new rules into existing ones, fetch current state with `deploy show` first.
