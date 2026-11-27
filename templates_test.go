package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func doTemplateJSONReq(t *testing.T, handler http.Handler, method, path, user, pass string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestTemplatesCRUD_OwnershipAndRoles(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	handler := srv.Handler()
	ctx := context.Background()

	// Create test users:
	// - alice: write (can create/edit/delete own templates, sees only her own)
	// - bob: write (sees only his own, cannot view/edit/delete alice's)
	// - viewer: read-only,template-viewer (sees all templates, cannot create/edit/delete)
	// - tadmin: template-admin (sees, edits, deletes all templates)
	for _, u := range []struct {
		name, pass, perm string
	}{
		{"admin", "adminpass", "all"},
		{"alice", "alicepass", "write"},
		{"bob", "bobpass", "write"},
		{"viewer", "viewerpass", "read-only,template-viewer"},
		{"tadmin", "tadminpass", "template-admin"},
	} {
		if _, err := srv.UpsertAPIUser(ctx, u.name, u.pass, u.perm, "test user", true); err != nil {
			t.Fatalf("upsert user %s: %v", u.name, err)
		}
	}

	// 1. Alice creates a pipeline template
	rr := doTemplateJSONReq(t, handler, http.MethodPost, "/api/v1/templates", "alice", "alicepass", map[string]any{
		"type":        "pipeline",
		"name":        "nightly_etl",
		"description": "Alice's ETL template",
		"content":     "<pipeline name=\"nightly_etl\"><flow/></pipeline>",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("alice create template: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var aliceTmpl TemplateRecord
	if err := json.Unmarshal(rr.Body.Bytes(), &aliceTmpl); err != nil {
		t.Fatalf("decode alice template: %v", err)
	}
	if aliceTmpl.ID <= 0 || aliceTmpl.CreatedBy != "alice" || aliceTmpl.UpdatedBy != "alice" || aliceTmpl.CreatedAt.IsZero() {
		t.Fatalf("unexpected alice template metadata: %+v", aliceTmpl)
	}

	// 2. Duplicate (type, name) for alice without overwrite -> 409 Conflict
	rr = doTemplateJSONReq(t, handler, http.MethodPost, "/api/v1/templates", "alice", "alicepass", map[string]any{
		"type":    "pipeline",
		"name":    "nightly_etl",
		"content": "<pipeline name=\"v2\"><flow/></pipeline>",
	})
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate template, got %d", rr.Code)
	}

	// 3. Bob CAN create his own template with the exact same (type, name)
	rr = doTemplateJSONReq(t, handler, http.MethodPost, "/api/v1/templates", "bob", "bobpass", map[string]any{
		"type":        "pipeline",
		"name":        "nightly_etl",
		"description": "Bob's ETL template",
		"content":     "<pipeline name=\"bob_etl\"><flow/></pipeline>",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("bob create same-name template: status=%d body=%s", rr.Code, rr.Body.String())
	}

	// 4. Bob lists templates -> sees ONLY his own (count == 1)
	rr = doTemplateJSONReq(t, handler, http.MethodGet, "/api/v1/templates", "bob", "bobpass", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("bob list templates: status=%d", rr.Code)
	}
	var listResp struct {
		Count     int              `json:"count"`
		Templates []TemplateRecord `json:"templates"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &listResp)
	if listResp.Count != 1 || listResp.Templates[0].CreatedBy != "bob" {
		t.Fatalf("bob should only see his own template, got %+v", listResp)
	}

	// 5. Bob tries to GET, PUT, DELETE, or instantiate Alice's template -> 403 Forbidden
	alicePath := "/api/v1/templates/" + strconv.FormatInt(aliceTmpl.ID, 10)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, alicePath, nil},
		{http.MethodPut, alicePath, map[string]any{"description": "hacked"}},
		{http.MethodDelete, alicePath, nil},
		{http.MethodPost, alicePath + "/instantiate", map[string]any{"name": "stolen"}},
	} {
		rr = doTemplateJSONReq(t, handler, tc.method, tc.path, "bob", "bobpass", tc.body)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("bob %s %s expected 403, got %d (%s)", tc.method, tc.path, rr.Code, rr.Body.String())
		}
	}

	// 6. template-viewer sees ALL templates (alice + bob), can GET alice's, cannot create/edit/delete
	rr = doTemplateJSONReq(t, handler, http.MethodGet, "/api/v1/templates", "viewer", "viewerpass", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("viewer list: status=%d", rr.Code)
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &listResp)
	if listResp.Count != 2 {
		t.Fatalf("template-viewer should see 2 templates, got %d", listResp.Count)
	}
	rr = doTemplateJSONReq(t, handler, http.MethodGet, alicePath, "viewer", "viewerpass", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("template-viewer GET alice template expected 200, got %d", rr.Code)
	}
	rr = doTemplateJSONReq(t, handler, http.MethodPut, alicePath, "viewer", "viewerpass", map[string]any{"description": "no"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("template-viewer PUT expected 403, got %d", rr.Code)
	}

	// 7. template-admin can edit Alice's template -> UpdatedBy becomes "tadmin", CreatedBy stays "alice"
	rr = doTemplateJSONReq(t, handler, http.MethodPut, alicePath, "tadmin", "tadminpass", map[string]any{
		"description": "Reviewed by template-admin",
		"content":     "<pipeline name=\"nightly_etl_v2\"><flow/></pipeline>",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("tadmin PUT expected 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	var updated TemplateRecord
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	if updated.CreatedBy != "alice" || updated.UpdatedBy != "tadmin" {
		t.Fatalf("expected CreatedBy=alice UpdatedBy=tadmin, got %+v", updated)
	}

	// 8. Admin instantiates Alice's pipeline template as a live pipeline
	rr = doTemplateJSONReq(t, handler, http.MethodPost, alicePath+"/instantiate", "admin", "adminpass", map[string]any{
		"name":        "prod_nightly_etl",
		"description": "Live production pipeline",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("admin instantiate expected 201, got %d (%s)", rr.Code, rr.Body.String())
	}
	var createdArt ArtifactRecord
	_ = json.Unmarshal(rr.Body.Bytes(), &createdArt)
	if createdArt.Name != "prod_nightly_etl" || createdArt.Type != "pipeline" {
		t.Fatalf("unexpected instantiated artifact: %+v", createdArt)
	}

	// 9. Strict ACL enforcement: Alice (write, no pipeline create ACL) cannot instantiate a live pipeline
	rr = doTemplateJSONReq(t, handler, http.MethodPost, alicePath+"/instantiate", "alice", "alicepass", map[string]any{
		"name": "blocked_pipeline",
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when instantiating pipeline without ACL, got %d (%s)", rr.Code, rr.Body.String())
	}

	// 10. Alice can create and instantiate a config template (non-pipeline uses global write perm)
	rr = doTemplateJSONReq(t, handler, http.MethodPost, "/api/v1/templates", "alice", "alicepass", map[string]any{
		"type":        "config",
		"name":        "db_defaults",
		"description": "Shared DB config",
		"content":     "<pipeline><variables/></pipeline>",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("alice create config template: status=%d", rr.Code)
	}
	var cfgTmpl TemplateRecord
	_ = json.Unmarshal(rr.Body.Bytes(), &cfgTmpl)
	rr = doTemplateJSONReq(t, handler, http.MethodPost, "/api/v1/templates/"+strconv.FormatInt(cfgTmpl.ID, 10)+"/instantiate", "alice", "alicepass", map[string]any{
		"name": "live_db_config",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("alice instantiate config template expected 201, got %d (%s)", rr.Code, rr.Body.String())
	}

	// 11. Effective permissions exposes template flags
	rr = doTemplateJSONReq(t, handler, http.MethodGet, "/api/v1/permissions/effective", "viewer", "viewerpass", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("effective perms status=%d", rr.Code)
	}
	var eff EffectivePermissionsSummary
	_ = json.Unmarshal(rr.Body.Bytes(), &eff)
	if !eff.CanViewAllTemplates || eff.CanManageAllTemplates || eff.CanWriteTemplates {
		t.Fatalf("unexpected viewer template perms: %+v", eff)
	}

	// 12. template-admin can delete Alice's template
	rr = doTemplateJSONReq(t, handler, http.MethodDelete, alicePath, "tadmin", "tadminpass", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("tadmin delete expected 200, got %d", rr.Code)
	}
}

func TestRenderTemplateDDL_AllDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres", "sqlserver", "mysql", "oracle"} {
		ddl, err := RenderTemplateDDL(dialect)
		if err != nil {
			t.Fatalf("RenderTemplateDDL(%q) error: %v", dialect, err)
		}
		for _, mustHave := range []string{"flow_template_content", "CreatedBy", "UpdatedBy", "TemplateXML"} {
			if !strings.Contains(ddl, mustHave) {
				t.Errorf("RenderTemplateDDL(%q) missing %q", dialect, mustHave)
			}
		}
	}
}
