package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
)

// capturedReq records one request the mock API received, with its JSON body
// decoded for assertions.
type capturedReq struct {
	Method string
	Path   string
	Query  url.Values
	Body   map[string]any
}

// resetOrgMgmtFlags zeroes every package-level flag var the org-management
// commands read, so a value set by one test never bleeds into the next (RunE is
// invoked directly, bypassing cobra's per-run flag reset).
func resetOrgMgmtFlags() {
	roleDescFlag, roleScopeFlag, rolePermissionsFlag = "", "", ""
	roleUpdateNameFlag, roleUpdateDescFlag, roleUpdatePermissionsFlag = "", "", ""
	memberTypeFlag = ""
	memberAddProviderFlag, memberAddHandleFlag, memberAddUIDFlag = "", "", ""
	memberAddRoleFlag, memberAddNameFlag, memberAddEmailFlag = "", "", ""
	groupDescFlag, groupMembersFlag = "", ""
	groupUpdateNameFlag, groupUpdateDescFlag, groupAddFlag, groupRemoveFlag = "", "", "", ""
	saCreateDescFlag, saUpdateNameFlag, saUpdateDescFlag = "", "", ""
	permissionScopeFlag = ""
}

// apiMock starts an httptest server wired into the client (via the base-URL
// seam) and config, records every request, and captures command stdout/stderr.
// handler serves each request. Returns the recorded requests + captured buffers.
func apiMock(t *testing.T, handler http.HandlerFunc) (reqs *[]capturedReq, stdout, stderr *bytes.Buffer) {
	t.Helper()
	resetOrgMgmtFlags()

	var recorded []capturedReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if len(bytes.TrimSpace(body)) > 0 {
			_ = json.Unmarshal(body, &parsed)
		}
		recorded = append(recorded, capturedReq{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.Query(),
			Body:   parsed,
		})
		r.Body = io.NopCloser(bytes.NewReader(body)) // restore for the handler
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	client.SetBaseURLForTest(srv.URL)
	t.Cleanup(func() { client.SetBaseURLForTest("") })

	t.Setenv("SEMAPHORE_API_TOKEN", "test-token")
	t.Setenv("SEMAPHORE_HOST", "example.test")
	config.Load()

	var out, errb bytes.Buffer
	output.SetWriters(&out, &errb)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	return &recorded, &out, &errb
}

// writeJSON is a tiny helper for mock handlers.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// find returns the first recorded request matching method+path, or fails.
func find(t *testing.T, reqs *[]capturedReq, method, path string) capturedReq {
	t.Helper()
	for _, r := range *reqs {
		if r.Method == method && r.Path == path {
			return r
		}
	}
	t.Fatalf("no %s %s request recorded; got %+v", method, path, *reqs)
	return capturedReq{}
}

// count returns how many recorded requests match method+path.
func count(reqs *[]capturedReq, method, path string) int {
	n := 0
	for _, r := range *reqs {
		if r.Method == method && r.Path == path {
			n++
		}
	}
	return n
}

// asStrings converts a decoded JSON array field to []string for comparison.
func asStrings(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, _ := e.(string)
		out = append(out, s)
	}
	return out
}

// ---- group update: the preserve fix (both directions) ----

func TestGroupUpdate_AddOnly_PreservesNameAndDescription(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/groups":
			writeJSON(w, 200, map[string]any{"groups": []map[string]any{
				{"id": "g1", "name": "backend", "description": "Backend team", "member_ids": []string{"u0"}},
			}})
		case r.Method == "PATCH" && r.URL.Path == "/api/v1alpha/groups/g1":
			writeJSON(w, 200, map[string]any{"id": "g1", "name": "backend", "description": "Backend team"})
		default:
			writeJSON(w, 500, map[string]any{"error": "unexpected " + r.Method + " " + r.URL.Path})
		}
	})

	groupAddFlag = "u1"
	if err := groupUpdateCmd.RunE(groupUpdateCmd, []string{"g1"}); err != nil {
		t.Fatalf("group update: %v", err)
	}

	patch := find(t, reqs, "PATCH", "/api/v1alpha/groups/g1")
	if patch.Body["name"] != "backend" {
		t.Errorf("name = %v, want preserved %q", patch.Body["name"], "backend")
	}
	if patch.Body["description"] != "Backend team" {
		t.Errorf("description = %v, want preserved %q", patch.Body["description"], "Backend team")
	}
	if got := asStrings(patch.Body["members_to_add"]); len(got) != 1 || got[0] != "u1" {
		t.Errorf("members_to_add = %v, want [u1]", got)
	}
	if _, sent := patch.Body["members_to_remove"]; sent {
		t.Errorf("members_to_remove should be absent when --remove unset; got %v", patch.Body["members_to_remove"])
	}
}

func TestGroupUpdate_NameOnly_PreservesDescription(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/groups":
			writeJSON(w, 200, map[string]any{"groups": []map[string]any{
				{"id": "g1", "name": "old", "description": "keep me", "member_ids": []string{}},
			}})
		case r.Method == "PATCH" && r.URL.Path == "/api/v1alpha/groups/g1":
			writeJSON(w, 200, map[string]any{"id": "g1"})
		default:
			writeJSON(w, 500, nil)
		}
	})

	groupUpdateNameFlag = "renamed"
	if err := groupUpdateCmd.RunE(groupUpdateCmd, []string{"g1"}); err != nil {
		t.Fatalf("group update: %v", err)
	}

	patch := find(t, reqs, "PATCH", "/api/v1alpha/groups/g1")
	if patch.Body["name"] != "renamed" {
		t.Errorf("name = %v, want %q", patch.Body["name"], "renamed")
	}
	if patch.Body["description"] != "keep me" {
		t.Errorf("description = %v, want preserved %q", patch.Body["description"], "keep me")
	}
}

func TestGroupUpdate_BothFlagsSet_DoesNotFetch(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" && r.URL.Path == "/api/v1alpha/groups/g1" {
			writeJSON(w, 200, map[string]any{"id": "g1"})
			return
		}
		// A GET here would mean we fetched needlessly.
		writeJSON(w, 500, nil)
	})

	groupUpdateNameFlag = "n"
	groupUpdateDescFlag = "d"
	if err := groupUpdateCmd.RunE(groupUpdateCmd, []string{"g1"}); err != nil {
		t.Fatalf("group update: %v", err)
	}
	if n := count(reqs, "GET", "/api/v1alpha/groups"); n != 0 {
		t.Errorf("expected no group fetch when both flags set; got %d GETs", n)
	}
	patch := find(t, reqs, "PATCH", "/api/v1alpha/groups/g1")
	if patch.Body["name"] != "n" || patch.Body["description"] != "d" {
		t.Errorf("body = %+v, want name=n description=d", patch.Body)
	}
}

func TestGroupUpdate_GroupNotFound_ErrorsAndNoPatch(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/v1alpha/groups" {
			writeJSON(w, 200, map[string]any{"groups": []map[string]any{
				{"id": "other", "name": "x", "description": "y"},
			}})
			return
		}
		writeJSON(w, 500, nil)
	})

	groupAddFlag = "u1"
	if err := groupUpdateCmd.RunE(groupUpdateCmd, []string{"g1"}); err == nil {
		t.Fatal("expected error when group not found, got nil")
	}
	if n := count(reqs, "PATCH", "/api/v1alpha/groups/g1"); n != 0 {
		t.Errorf("must NOT PATCH when group not found (would blank fields); got %d PATCHes", n)
	}
}

// ---- group create ----

func TestGroupCreate_Success(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"id": "g9", "name": "team"})
	})
	groupDescFlag = "desc"
	groupMembersFlag = "a,b"
	if err := groupCreateCmd.RunE(groupCreateCmd, []string{"team"}); err != nil {
		t.Fatalf("group create: %v", err)
	}
	req := find(t, reqs, "POST", "/api/v1alpha/groups")
	if req.Body["name"] != "team" || req.Body["description"] != "desc" {
		t.Errorf("body = %+v, want name=team description=desc", req.Body)
	}
	if got := asStrings(req.Body["member_ids"]); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("member_ids = %v, want [a b]", got)
	}
}

func TestGroupCreate_ServerError(t *testing.T) {
	_, _, errb := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 400, map[string]any{"message": "name required"})
	})
	if err := groupCreateCmd.RunE(groupCreateCmd, []string{"team"}); err == nil {
		t.Fatal("expected error on HTTP 400, got nil")
	}
	if !strings.Contains(errb.String(), "400") {
		t.Errorf("stderr should report the 400; got %q", errb.String())
	}
}

func TestGroupDelete_Success(t *testing.T) {
	reqs, out, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "deleted"})
	})
	if err := groupDeleteCmd.RunE(groupDeleteCmd, []string{"g1"}); err != nil {
		t.Fatalf("group delete: %v", err)
	}
	find(t, reqs, "DELETE", "/api/v1alpha/groups/g1")
	if !strings.Contains(out.String(), "deleted") {
		t.Errorf("stdout should confirm deletion; got %q", out.String())
	}
}

// ---- roles ----

func TestOrgRoleCreate_Success(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"id": "r1", "name": "deployer"})
	})
	roleScopeFlag = "org"
	rolePermissionsFlag = "project.view, project.job.rerun"
	if err := orgRoleCreateCmd.RunE(orgRoleCreateCmd, []string{"deployer"}); err != nil {
		t.Fatalf("role create: %v", err)
	}
	req := find(t, reqs, "POST", "/api/v1alpha/roles")
	if req.Body["name"] != "deployer" || req.Body["scope"] != "org" {
		t.Errorf("body = %+v, want name=deployer scope=org", req.Body)
	}
	if got := asStrings(req.Body["permissions"]); len(got) != 2 || got[0] != "project.view" || got[1] != "project.job.rerun" {
		t.Errorf("permissions = %v, want [project.view project.job.rerun]", got)
	}
}

func TestOrgRoleCreate_ServerError(t *testing.T) {
	_, _, _ = apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 403, map[string]any{"message": "forbidden"})
	})
	if err := orgRoleCreateCmd.RunE(orgRoleCreateCmd, []string{"deployer"}); err == nil {
		t.Fatal("expected error on HTTP 403, got nil")
	}
}

func TestOrgRoleUpdate_Success(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"id": "r1"})
	})
	roleUpdatePermissionsFlag = "project.view"
	if err := orgRoleUpdateCmd.RunE(orgRoleUpdateCmd, []string{"r1"}); err != nil {
		t.Fatalf("role update: %v", err)
	}
	req := find(t, reqs, "PATCH", "/api/v1alpha/roles/r1")
	if got := asStrings(req.Body["permissions"]); len(got) != 1 || got[0] != "project.view" {
		t.Errorf("permissions = %v, want [project.view]", got)
	}
	if _, sent := req.Body["name"]; sent {
		t.Errorf("name should be absent when --name unset (server backfills); got %v", req.Body["name"])
	}
}

func TestOrgRoleDelete_Success(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok"})
	})
	if err := orgRoleDeleteCmd.RunE(orgRoleDeleteCmd, []string{"r1"}); err != nil {
		t.Fatalf("role delete: %v", err)
	}
	find(t, reqs, "DELETE", "/api/v1alpha/roles/r1")
}

// ---- org members ----

func TestMemberSetRole_Success(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	if err := memberSetRoleCmd.RunE(memberSetRoleCmd, []string{"u1", "r1"}); err != nil {
		t.Fatalf("set-role: %v", err)
	}
	req := find(t, reqs, "PUT", "/api/v1alpha/members/u1/role")
	if req.Body["role_id"] != "r1" {
		t.Errorf("role_id = %v, want r1", req.Body["role_id"])
	}
}

func TestMemberRemove_Success_BareDelete(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	if err := memberRemoveCmd.RunE(memberRemoveCmd, []string{"u1"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	req := find(t, reqs, "DELETE", "/api/v1alpha/members/u1")
	// Full-org-removal contract: bare delete, no role_id (empty role_id on the
	// server falls through to "retract all bindings for the subject").
	if req.Query.Get("role_id") != "" {
		t.Errorf("did not expect role_id query param; got %q", req.Query.Get("role_id"))
	}
}

func TestMemberAdd_Success(t *testing.T) {
	reqs, _, errb := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"id": "u9", "role": map[string]any{"status": "assigned"}})
	})
	memberAddProviderFlag = "github"
	memberAddHandleFlag = "octocat"
	memberAddRoleFlag = "r1"
	if err := memberAddCmd.RunE(memberAddCmd, nil); err != nil {
		t.Fatalf("member add: %v", err)
	}
	req := find(t, reqs, "POST", "/api/v1alpha/members")
	if req.Body["provider"] != "github" || req.Body["handle"] != "octocat" || req.Body["role_id"] != "r1" {
		t.Errorf("body = %+v, want provider=github handle=octocat role_id=r1", req.Body)
	}
	if strings.Contains(errb.String(), "warning") {
		t.Errorf("no warning expected when role applied; got %q", errb.String())
	}
}

func TestMemberAdd_RoleNotApplied_Warns(t *testing.T) {
	_, _, errb := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"id": "u9", "role": map[string]any{"status": "denied"}})
	})
	memberAddProviderFlag = "github"
	memberAddHandleFlag = "octocat"
	memberAddRoleFlag = "r1"
	if err := memberAddCmd.RunE(memberAddCmd, nil); err != nil {
		t.Fatalf("member add: %v", err)
	}
	if !strings.Contains(errb.String(), "warning") || !strings.Contains(errb.String(), "denied") {
		t.Errorf("expected a role-not-applied warning; got %q", errb.String())
	}
}

func TestMemberAdd_NoRoleRequested_NoWarn(t *testing.T) {
	_, _, errb := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"id": "u9", "role": map[string]any{"status": "defaulted_to_member"}})
	})
	memberAddProviderFlag = "github"
	memberAddHandleFlag = "octocat"
	// no --role: defaulting to member is expected, not a warning.
	if err := memberAddCmd.RunE(memberAddCmd, nil); err != nil {
		t.Fatalf("member add: %v", err)
	}
	if strings.Contains(errb.String(), "warning") {
		t.Errorf("no warning expected without --role; got %q", errb.String())
	}
}

func TestMemberList_TypeFilter(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, []map[string]any{{"id": "u1"}})
	})
	memberTypeFlag = "service_account"
	if err := orgMemberListCmd.RunE(orgMemberListCmd, nil); err != nil {
		t.Fatalf("member list: %v", err)
	}
	req := find(t, reqs, "GET", "/api/v1alpha/members")
	if req.Query.Get("member_type") != "service_account" {
		t.Errorf("member_type = %q, want service_account", req.Query.Get("member_type"))
	}
}

// ---- service accounts ----

func TestServiceAccountCreate_Success(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"id": "sa1", "api_token": "secret"})
	})
	saCreateDescFlag = "ci bot"
	if err := serviceAccountCreateCmd.RunE(serviceAccountCreateCmd, []string{"ci-bot"}); err != nil {
		t.Fatalf("sa create: %v", err)
	}
	req := find(t, reqs, "POST", "/api/v1alpha/service_accounts")
	if req.Body["name"] != "ci-bot" || req.Body["description"] != "ci bot" {
		t.Errorf("body = %+v, want name=ci-bot description='ci bot'", req.Body)
	}
}

func TestServiceAccountUpdate_DescriptionOnly_PreservesName(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/service_accounts/sa1":
			writeJSON(w, 200, map[string]any{"id": "sa1", "name": "ci-bot"})
		case r.Method == "PATCH" && r.URL.Path == "/api/v1alpha/service_accounts/sa1":
			writeJSON(w, 200, map[string]any{"id": "sa1"})
		default:
			writeJSON(w, 500, nil)
		}
	})
	saUpdateDescFlag = "new description"
	if err := serviceAccountUpdateCmd.RunE(serviceAccountUpdateCmd, []string{"sa1"}); err != nil {
		t.Fatalf("sa update: %v", err)
	}
	patch := find(t, reqs, "PATCH", "/api/v1alpha/service_accounts/sa1")
	if patch.Body["name"] != "ci-bot" {
		t.Errorf("name = %v, want preserved ci-bot", patch.Body["name"])
	}
	if patch.Body["description"] != "new description" {
		t.Errorf("description = %v, want 'new description'", patch.Body["description"])
	}
}

func TestServiceAccountUpdate_NothingToUpdate_Errors(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 500, nil) // should never be hit
	})
	if err := serviceAccountUpdateCmd.RunE(serviceAccountUpdateCmd, []string{"sa1"}); err == nil {
		t.Fatal("expected error when neither --name nor --description given")
	}
	if len(*reqs) != 0 {
		t.Errorf("expected no HTTP calls; got %+v", *reqs)
	}
}

func TestServiceAccountDeactivate_Success(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	if err := serviceAccountDeactivateCmd.RunE(serviceAccountDeactivateCmd, []string{"sa1"}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	find(t, reqs, "POST", "/api/v1alpha/service_accounts/sa1/deactivate")
}

func TestServiceAccountRegenerateToken_Success(t *testing.T) {
	reqs, out, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"api_token": "new-token"})
	})
	if err := serviceAccountRegenerateTokenCmd.RunE(serviceAccountRegenerateTokenCmd, []string{"sa1"}); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	find(t, reqs, "POST", "/api/v1alpha/service_accounts/sa1/regenerate_token")
	if !strings.Contains(out.String(), "new-token") {
		t.Errorf("stdout should include the new token; got %q", out.String())
	}
}

func TestServiceAccountDelete_ServerError(t *testing.T) {
	_, _, _ = apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]any{"message": "not found"})
	})
	if err := serviceAccountDeleteCmd.RunE(serviceAccountDeleteCmd, []string{"sa1"}); err == nil {
		t.Fatal("expected error on HTTP 404, got nil")
	}
}

// ---- project members (routes through resolveProjectID) ----

func TestProjectMemberSetRole_Success(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects/my-project":
			writeJSON(w, 200, map[string]any{"metadata": map[string]any{"id": "proj-1"}})
		case r.Method == "PUT" && r.URL.Path == "/api/v1alpha/projects/proj-1/members/u1/role":
			writeJSON(w, 200, map[string]any{"ok": true})
		default:
			writeJSON(w, 500, map[string]any{"path": r.URL.Path})
		}
	})
	if err := projectMemberSetRoleCmd.RunE(projectMemberSetRoleCmd, []string{"my-project", "u1", "r1"}); err != nil {
		t.Fatalf("project set-role: %v", err)
	}
	req := find(t, reqs, "PUT", "/api/v1alpha/projects/proj-1/members/u1/role")
	if req.Body["role_id"] != "r1" {
		t.Errorf("role_id = %v, want r1", req.Body["role_id"])
	}
}

func TestProjectMemberRemove_Success(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects/my-project":
			writeJSON(w, 200, map[string]any{"metadata": map[string]any{"id": "proj-1"}})
		case r.Method == "DELETE" && r.URL.Path == "/api/v1alpha/projects/proj-1/members/u1/role":
			writeJSON(w, 200, map[string]any{"ok": true})
		default:
			writeJSON(w, 500, nil)
		}
	})
	if err := projectMemberRemoveCmd.RunE(projectMemberRemoveCmd, []string{"my-project", "u1"}); err != nil {
		t.Fatalf("project remove: %v", err)
	}
	find(t, reqs, "DELETE", "/api/v1alpha/projects/proj-1/members/u1/role")
}

// ---- permissions ----

func TestPermissionList_ScopeFilter(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, []map[string]any{{"name": "project.view"}})
	})
	permissionScopeFlag = "project"
	if err := permissionListCmd.RunE(permissionListCmd, nil); err != nil {
		t.Fatalf("permission list: %v", err)
	}
	req := find(t, reqs, "GET", "/api/v1alpha/permissions")
	if req.Query.Get("scope") != "project" {
		t.Errorf("scope = %q, want project", req.Query.Get("scope"))
	}
}
