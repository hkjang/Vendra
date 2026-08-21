package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hkjang/Vendra/internal/config"
	"github.com/hkjang/Vendra/internal/observability"
	"github.com/hkjang/Vendra/internal/security"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

type App struct {
	db        *pgxpool.Pool
	vault     *security.Vault
	auth      authService
	audit     auditor
	logs      *observability.Store
	staticDir string
}

func New(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, staticDir string) (*App, error) {
	vault, err := security.NewVault(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	if err := bootstrapAdmin(ctx, pool, cfg.BootstrapAdmin, cfg.BootstrapAdminPassword); err != nil {
		return nil, fmt.Errorf("bootstrap administrator: %w", err)
	}
	app := &App{db: pool, vault: vault, auth: authService{db: pool}, audit: auditor{db: pool}, logs: observability.DefaultStore(), staticDir: staticDir}
	go app.runBackground(ctx)
	return app, nil
}

func (a *App) Handler() http.Handler {
	root := http.NewServeMux()
	root.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]any{"status": "ok"}) })
	root.HandleFunc("GET /health/ready", a.ready)
	root.HandleFunc("GET /metrics", a.metrics)
	root.HandleFunc("GET /api/version", a.version)
	root.HandleFunc("POST /api/auth/login", a.auth.login)
	root.HandleFunc("POST /api/auth/logout", a.auth.logout)
	root.HandleFunc("POST /api/auth/register", a.registerSupplierUser)
	root.HandleFunc("GET /api/auth/verify-email", a.verifyEmail)
	root.HandleFunc("GET /api/auth/oidc/config", a.oidcPublicConfig)
	root.HandleFunc("GET /api/auth/oidc/start", a.oidcStart)
	root.HandleFunc("GET /api/auth/oidc/callback", a.oidcCallback)

	api := http.NewServeMux()
	a.registerAPI(api)
	root.Handle("/api/v1/", a.auth.middleware(true, api))
	root.Handle("/mcp", a.auth.middleware(true, http.HandlerFunc(a.mcp)))
	root.Handle("/mcp/", a.auth.middleware(true, http.HandlerFunc(a.mcp)))
	root.HandleFunc("/", a.serveSPA)
	return requestMiddleware(recoverMiddleware(root))
}

func (a *App) registerAPI(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/me", a.me)
	m.HandleFunc("PATCH /api/v1/me", a.updateMe)
	m.HandleFunc("GET /api/v1/me/api-keys", a.listAPIKeys)
	m.HandleFunc("POST /api/v1/me/api-keys", a.createAPIKey)
	m.HandleFunc("POST /api/v1/me/api-keys/{id}/rotate", a.rotateAPIKey)
	m.HandleFunc("DELETE /api/v1/me/api-keys/{id}", a.revokeAPIKey)
	m.HandleFunc("GET /api/v1/me/notifications", a.listNotifications)
	m.HandleFunc("POST /api/v1/me/notifications/read-all", a.readAllNotifications)
	m.HandleFunc("POST /api/v1/me/notifications/{id}/read", a.readNotification)
	m.HandleFunc("GET /api/v1/me/work-inbox", a.workInbox)
	m.HandleFunc("POST /api/v1/me/work-items/state", a.updateWorkItemState)
	m.HandleFunc("GET /api/v1/me/saved-views", a.listSavedViews)
	m.HandleFunc("POST /api/v1/me/saved-views", a.createSavedView)
	m.HandleFunc("PATCH /api/v1/me/saved-views/{id}", a.updateSavedView)
	m.HandleFunc("DELETE /api/v1/me/saved-views/{id}", a.deleteSavedView)
	m.HandleFunc("GET /api/v1/me/drafts/{key}", a.getFormDraft)
	m.HandleFunc("PUT /api/v1/me/drafts/{key}", a.putFormDraft)
	m.HandleFunc("DELETE /api/v1/me/drafts/{key}", a.deleteFormDraft)

	m.HandleFunc("GET /api/v1/dashboard", require("dashboard.read", a.dashboard))
	m.HandleFunc("GET /api/v1/search", a.globalSearch)
	m.HandleFunc("GET /api/v1/suppliers", require("supplier.read", a.listSuppliers))
	m.HandleFunc("POST /api/v1/suppliers", require("supplier.create", a.createSupplier))
	m.HandleFunc("GET /api/v1/suppliers/{id}", require("supplier.read", a.getSupplier))
	m.HandleFunc("PATCH /api/v1/suppliers/{id}", require("supplier.update", a.updateSupplier))
	m.HandleFunc("GET /api/v1/suppliers/{id}/activity", require("supplier.read", a.supplierActivity))
	m.HandleFunc("GET /api/v1/suppliers/{id}/contacts", require("supplier.read", a.listContacts))
	m.HandleFunc("POST /api/v1/suppliers/{id}/contacts", require("supplier.update", a.createContact))
	m.HandleFunc("GET /api/v1/suppliers/{id}/objects", require("supplier.read", a.supplierObjects))
	m.HandleFunc("GET /api/v1/suppliers/{id}/risks", require("supplier.read", a.listSupplierRisks))
	m.HandleFunc("POST /api/v1/suppliers/{id}/risks", require("risk.create", a.createRisk))
	m.HandleFunc("GET /api/v1/suppliers/{id}/evaluations", require("supplier.read", a.listEvaluations))
	m.HandleFunc("POST /api/v1/suppliers/{id}/evaluations", require("evaluation.create", a.createEvaluation))
	m.HandleFunc("GET /api/v1/evaluations", require("evaluation.read", a.listAllEvaluations))
	m.HandleFunc("GET /api/v1/risks", require("risk.read", a.listAllRisks))
	m.HandleFunc("GET /api/v1/spend", require("spend.read", a.spendAnalysis))
	m.HandleFunc("GET /api/v1/supplier-network", require("supplier.read", a.supplierNetwork))
	m.HandleFunc("POST /api/v1/supplier-network/relationships", require("supplier.update", a.createSupplierRelationship))
	m.HandleFunc("GET /api/v1/suppliers/{id}/screenings", require("supplier.read", a.listScreenings))
	m.HandleFunc("GET /api/v1/screening-templates", require("supplier.read", a.listScreeningTemplates))
	m.HandleFunc("GET /api/v1/scorecards", require("supplier.read", a.listScorecards))
	m.HandleFunc("POST /api/v1/suppliers/{id}/screenings", require("supplier.update", a.createScreening))
	m.HandleFunc("PATCH /api/v1/screenings/{id}", require("supplier.update", a.updateScreening))
	m.HandleFunc("GET /api/v1/sourcing/{id}/participants", require("rfq.read", a.listSourcingParticipants))
	m.HandleFunc("POST /api/v1/sourcing/{id}/participants", require("rfq.update", a.addSourcingParticipants))
	m.HandleFunc("GET /api/v1/sourcing/{id}/comparison", require("rfq.read", a.sourcingComparison))
	m.HandleFunc("POST /api/v1/sourcing/responses/{id}/evaluate", require("rfq.update", a.evaluateSourcingResponse))
	m.HandleFunc("GET /api/v1/sourcing/{id}/questions", require("rfq.read", a.listSourcingQuestions))
	m.HandleFunc("POST /api/v1/sourcing/{id}/questions", require("rfq.update", a.createInternalSourcingQuestion))
	m.HandleFunc("PATCH /api/v1/sourcing/questions/{id}/answer", require("rfq.update", a.answerSourcingQuestion))
	m.HandleFunc("GET /api/v1/sourcing/{id}/committee", require("rfq.read", a.listSourcingCommittee))
	m.HandleFunc("POST /api/v1/sourcing/{id}/committee", require("rfq.update", a.addSourcingCommittee))
	m.HandleFunc("GET /api/v1/sourcing-committee-candidates", require("rfq.update", a.listSourcingCommitteeCandidates))
	m.HandleFunc("POST /api/v1/sourcing/{id}/select", require("rfq.update", a.selectSourcingResponse))
	m.HandleFunc("POST /api/v1/spend/transactions", require("spend.create", a.createSpendTransaction))
	m.HandleFunc("GET /api/v1/documents", require("document.read", a.listDocuments))
	m.HandleFunc("POST /api/v1/documents/upload", require("document.create", a.uploadDocument))
	m.HandleFunc("GET /api/v1/documents/{id}/download", require("document.read", a.downloadDocument))
	m.HandleFunc("GET /api/v1/documents/{id}/preview", require("document.read", a.previewDocument))
	m.HandleFunc("GET /api/v1/documents/{id}/signatures", require("document.read", a.listDocumentSignatures))
	m.HandleFunc("POST /api/v1/documents/{id}/signatures", require("document.update", a.signDocument))

	for _, route := range objectRoutes {
		typeName := route.objectType
		base := route.path
		m.HandleFunc("GET "+base, require(typeName+".read", a.listObjects(typeName)))
		m.HandleFunc("POST "+base, require(typeName+".create", a.createObject(typeName)))
		m.HandleFunc("GET "+base+"/{id}", require(typeName+".read", a.getObject(typeName)))
		m.HandleFunc("PATCH "+base+"/{id}", require(typeName+".update", a.updateObject(typeName)))
		m.HandleFunc("POST "+base+"/{id}/submit", require(typeName+".update", a.submitObject(typeName)))
	}

	m.HandleFunc("GET /api/v1/workflows", require("workflow.read", a.listWorkflows))
	m.HandleFunc("POST /api/v1/workflows", require("workflow.create", a.createWorkflow))
	m.HandleFunc("PATCH /api/v1/workflows/{id}", require("workflow.update", a.updateWorkflow))
	m.HandleFunc("GET /api/v1/approvals", require("workflow.read", a.listApprovals))
	m.HandleFunc("POST /api/v1/approvals/{id}/actions", require("workflow.approve", a.workflowAction))

	m.HandleFunc("GET /api/v1/admin/settings", require("*", a.listSettings))
	m.HandleFunc("PUT /api/v1/admin/settings/{key}", require("*", a.putSetting))
	m.HandleFunc("GET /api/v1/admin/users", require("*", a.listUsers))
	m.HandleFunc("POST /api/v1/admin/users", require("*", a.createUser))
	m.HandleFunc("PATCH /api/v1/admin/users/{id}", require("*", a.updateUser))
	m.HandleFunc("GET /api/v1/admin/roles", require("*", a.listRoles))
	m.HandleFunc("POST /api/v1/admin/roles", require("*", a.createRole))
	m.HandleFunc("PATCH /api/v1/admin/roles/{id}", require("*", a.updateRole))
	m.HandleFunc("GET /api/v1/admin/organizations", require("*", a.listOrganizations))
	m.HandleFunc("POST /api/v1/admin/organizations", require("*", a.createOrganization))
	m.HandleFunc("GET /api/v1/admin/access-grants", require("*", a.listAccessGrants))
	m.HandleFunc("POST /api/v1/admin/access-grants", require("*", a.createAccessGrant))
	m.HandleFunc("DELETE /api/v1/admin/access-grants/{id}", require("*", a.deleteAccessGrant))
	m.HandleFunc("GET /api/v1/admin/lifecycle", require("*", a.listLifecycle))
	m.HandleFunc("PUT /api/v1/admin/lifecycle/{entityType}", require("*", a.putLifecycle))
	m.HandleFunc("GET /api/v1/admin/audit", require("audit.read", a.listAudit))
	m.HandleFunc("GET /api/v1/admin/logs", require("*", a.listServerLogs))
	m.HandleFunc("GET /api/v1/admin/scorecards", require("*", a.listScorecards))
	m.HandleFunc("POST /api/v1/admin/scorecards", require("*", a.createScorecard))
	m.HandleFunc("GET /api/v1/admin/screening-templates", require("*", a.listScreeningTemplates))
	m.HandleFunc("POST /api/v1/admin/screening-templates", require("*", a.createScreeningTemplate))

	m.HandleFunc("GET /api/v1/portal/profile", require("portal.*", a.portalProfile))
	m.HandleFunc("PATCH /api/v1/portal/profile", require("portal.*", a.portalUpdateProfile))
	m.HandleFunc("GET /api/v1/portal/contacts", require("portal.*", a.portalContacts))
	m.HandleFunc("POST /api/v1/portal/contacts", require("portal.*", a.portalCreateContact))
	m.HandleFunc("POST /api/v1/portal/contacts/{id}/verification", require("portal.*", a.portalRequestContactVerification))
	m.HandleFunc("GET /api/v1/portal/work", require("portal.*", a.portalWork))
	m.HandleFunc("GET /api/v1/portal/evaluations", require("portal.*", a.portalEvaluations))
	m.HandleFunc("POST /api/v1/portal/inquiries", require("portal.*", a.portalCreateBusinessObject("issue")))
	m.HandleFunc("GET /api/v1/portal/documents", require("portal.*", a.portalDocuments))
	m.HandleFunc("POST /api/v1/portal/documents/upload", require("portal.*", a.portalUploadDocument))
	m.HandleFunc("GET /api/v1/portal/sourcing", require("portal.*", a.portalSourcing))
	m.HandleFunc("PUT /api/v1/portal/sourcing/{id}/response", require("portal.*", a.portalSourcingResponse))
	m.HandleFunc("POST /api/v1/portal/sourcing/{id}/decline", require("portal.*", a.portalDeclineSourcing))
	m.HandleFunc("GET /api/v1/portal/sourcing/{id}/questions", require("portal.*", a.portalSourcingQuestions))
	m.HandleFunc("POST /api/v1/portal/sourcing/{id}/questions", require("portal.*", a.portalAskSourcingQuestion))
	m.HandleFunc("POST /api/v1/portal/purchase-orders/{id}/confirm", require("portal.*", a.portalConfirmPurchaseOrder))
	m.HandleFunc("POST /api/v1/portal/contracts/{id}/confirm", require("portal.*", a.portalConfirmContract))
	m.HandleFunc("POST /api/v1/portal/deliveries", require("portal.*", a.portalCreateBusinessObject("delivery")))
	m.HandleFunc("POST /api/v1/portal/invoices", require("portal.*", a.portalCreateBusinessObject("invoice")))

	m.HandleFunc("POST /api/v1/invitations", require("supplier.update", a.createInvitation))
	m.HandleFunc("POST /api/v1/ai/analyze", require("ai.use", a.aiAnalyze))
	m.HandleFunc("POST /api/v1/ai/contracts/{id}/analyze", require("ai.use", a.aiAnalyzeContract))
	m.HandleFunc("GET /api/v1/openapi.json", a.openapi)
}

func (a *App) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.db.Ping(ctx); err != nil {
		writeJSON(w, 503, map[string]any{"status": "not_ready"})
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ready"})
}

func (a *App) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]string{"name": "Vendra", "version": Version, "commit": Commit, "buildTime": BuildTime})
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	writeJSON(w, 200, p)
}

func (a *App) updateMe(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		DisplayName string `json:"displayName"`
		Locale      string `json:"locale"`
		Timezone    string `json:"timezone"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(in.DisplayName) == "" {
		writeError(w, 400, "validation_error", "이름은 필수입니다")
		return
	}
	_, err := a.db.Exec(r.Context(), `UPDATE users SET display_name=$2,locale=COALESCE(NULLIF($3,''),locale),timezone=COALESCE(NULLIF($4,''),timezone),updated_at=now() WHERE id=$1`, p.ID, in.DisplayName, in.Locale, in.Timezone)
	if err != nil {
		writeError(w, 500, "database_error", "프로필을 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "update", "user", p.ID, nil, in)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) serveSPA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, 404, "not_found", "찾을 수 없습니다")
		return
	}
	clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if clean == "." {
		clean = "index.html"
	}
	base, err := filepath.Abs(a.staticDir)
	if err != nil {
		http.Error(w, "UI unavailable", 503)
		return
	}
	target := filepath.Join(base, clean)
	if !strings.HasPrefix(target, base+string(os.PathSeparator)) && target != base {
		writeError(w, 404, "not_found", "찾을 수 없습니다")
		return
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		target = filepath.Join(base, "index.html")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		http.Error(w, "Vendra UI has not been built", http.StatusServiceUnavailable)
		return
	}
	if ct := mime.TypeByExtension(filepath.Ext(target)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if filepath.Base(target) == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public,max-age=31536000,immutable")
	}
	_, _ = w.Write(data)
}

func decodeMap(r *http.Request) (map[string]any, error) {
	var v map[string]any
	err := decodeJSON(r, &v)
	return v, err
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func nullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return strings.TrimSpace(v)
}
func isNotFound(err error) bool { return errors.Is(err, fs.ErrNotExist) }

func logDB(err error) { slog.Error("database error", "error", err) }
