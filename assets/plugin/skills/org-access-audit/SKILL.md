---
name: org-access-audit
description: "Audit an organization's access posture (members, roles, groups, service accounts) against least-privilege and report ranked findings with a proposed remediation plan. Read-only — proposes fixes, never applies them."
user-invocable: true
---

# Org Access Audit

Review who can do what in a Semaphore organization and flag least-privilege
problems: over-privileged service accounts, too many admins, orphaned
credentials, broad or dead groups, dangerous role combinations.

**This skill is read-only.** It runs only `list` / `show` commands and writes a
report. It **proposes** remediation commands but **never runs** any mutating
command (no create/update/delete/assign/retract). The human reviews and runs the
plan themselves.

## Step 0 — Confirm the target

Run `sem-ai context show` and state, at the top of the report, which **org/host**
is being audited. An audit that doesn't name its subject is useless.

## Step 1 — Inventory (read-only)

Gather the org's access data. Every command emits JSON.

```bash
sem-ai context show                            # org + host being audited
sem-ai org member list                         # users + their org roles
sem-ai org member list --type service_account  # service accounts + their org roles
sem-ai org member list --type group            # groups + their org roles
sem-ai org role list                           # available org roles WITH their permissions inline
sem-ai group list                              # groups + member_ids
sem-ai service-account list                    # SAs: deactivated?, creator_id, created/updated
sem-ai permission list                         # permission catalogue (names are stable; ids are per-org)
```

`org member list` defaults to users; `--type service_account` / `--type group`
surface those subjects with their role bindings — essential for catching an
over-privileged service account and for seeing which role each group confers.

`org role list` already returns each role's permissions inline, so a separate
`sem-ai org role show <role-id>` is only needed to re-check a single role.

Optionally, for project-scoped review: `sem-ai project member list <project-id>`.

## Step 2 — Build the access picture

From the JSON, assemble:

- every **subject** (user or service account) → its org role(s) → effective permissions;
- **group** → the role it confers on members (read it from `org member list --type group`; groups get **Member** by default) → its members, and whether each fits the group's purpose;
- **service accounts** → role, `deactivated`, `creator_id`, age;
- map `creator_id` / `member_ids` back to the member list to spot orphans and strays.

Reference roles and permissions **by name**, not id — ids are generated per org
and are not portable.

## Step 3 — Findings taxonomy

Classify each issue. Be specific: cite the subject and the evidence.

**HIGH**
- A **service account holding Owner or Admin** — an org-wide admin credential is a large blast radius; SAs should be narrowly scoped.
- A **custom role that combines people-management with billing/finance management** (separation-of-duties violation).
- A subject sitting in a group that grants **broad/admin permissions** where its presence looks unintended (e.g. a service account in an admins group).
- A **group whose role binding is Owner or Admin** — every member silently inherits it, so adding anyone to that group is an escalation path; an Owner-granting group is the most dangerous.

**MEDIUM**
- **More than 3 Owners/Admins** — report the count and who; recommend trimming.
- An **active service account whose creator is no longer an org member** (orphaned credential).
- A **broad-permission group whose members don't match its stated purpose**.

**LOW**
- **Deactivated service accounts never deleted** (credential hygiene).
- **Empty groups** (0 members) or single-member groups (dead / over-specific).
- A **direct role assignment that duplicates** a role the subject already gets via a group (redundant grant).

## Step 4 — Report

Produce one markdown report:

1. **Summary** — org/host; counts of members, owners/admins, groups, service accounts (active vs deactivated), custom roles.
2. **Findings** — a table sorted by severity: `Severity | Subject | Issue | Evidence | Recommended action`.
3. **Proposed remediation plan** — the exact `sem-ai` commands that *would* fix each finding, in order (e.g. `sem-ai org member assign-role <sa-id> <Member-role-id>` to downgrade an over-privileged SA; `sem-ai service-account delete <id>` for a stale SA; `sem-ai group delete <id>` for a dead group). Prefix the section with: **"Review before running — this skill does not execute these."**

## Guardrails

- **Read-only.** Only `context show`, `*  list`, `* show`. If a fix is warranted, it goes in the proposed plan — never executed.
- Do not print API tokens (the `list` commands don't return them; keep it that way).
- If a command errors (e.g. a feature flag is off or the org has no groups), note it in the report and continue — a partial audit is still useful.
