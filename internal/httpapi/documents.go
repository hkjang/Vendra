package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type storageSettings struct {
	Driver string `json:"driver"`
	Path   string `json:"path"`
}

func (a *App) storagePath(r *http.Request) (string, error) {
	var value []byte
	if err := a.db.QueryRow(r.Context(), `SELECT value FROM settings WHERE key='storage'`).Scan(&value); err != nil {
		return "", err
	}
	var s storageSettings
	if err := json.Unmarshal(value, &s); err != nil {
		return "", err
	}
	if s.Driver != "filesystem" || !filepath.IsAbs(s.Path) {
		return "", fmt.Errorf("filesystem storage is not configured")
	}
	return s.Path, nil
}

// documentLive is the set of statuses a document can still be used in. Approval
// is a signature outcome, not an end state: gating retrieval on 'active' alone
// made an approved document impossible to download, preview or countersign,
// while it stayed visible in the list.
const documentLive = `status IN ('active','approved')`

func (a *App) uploadDocument(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 26<<20)
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		writeError(w, 400, "file_too_large", "파일은 25MB 이하여야 합니다")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "file_required", "업로드할 파일이 필요합니다")
		return
	}
	defer file.Close()
	p, _ := principalFrom(r.Context())
	if supplierID := r.FormValue("supplierId"); supplierID != "" {
		if p.UserType == "supplier" {
			if p.SupplierID == nil || *p.SupplierID != supplierID {
				writeError(w, 403, "portal_scope", "다른 공급업체에 문서를 등록할 수 없습니다")
				return
			}
		} else if !a.supplierScopeAllowed(r, supplierID) {
			writeError(w, 403, "data_scope", "문서 대상이 데이터 접근 범위를 벗어났습니다")
			return
		}
	}
	if objectID := r.FormValue("objectId"); objectID != "" && p.UserType != "supplier" {
		o, objectErr := scanObject(a.db.QueryRow(r.Context(), objectSelect+` WHERE o.id=$1 AND o.deleted_at IS NULL`, objectID))
		if objectErr != nil || (!a.canAccessObject(r.Context(), p, o) && !grantAuthorized(r.Context())) {
			writeError(w, 403, "data_scope", "문서 대상 업무가 데이터 접근 범위를 벗어났습니다")
			return
		}
	}
	root, err := a.storagePath(r)
	if err != nil {
		writeError(w, 503, "storage_unavailable", "파일 저장소가 설정되지 않았습니다")
		return
	}
	fileID, err := randomToken(18)
	if err != nil {
		writeError(w, 500, "storage_error", "파일 ID를 만들지 못했습니다")
		return
	}
	cleanName := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	if cleanName == "." || cleanName == "" {
		cleanName = "document"
	}
	if err := os.MkdirAll(root, 0750); err != nil {
		writeError(w, 500, "storage_error", "파일 저장소를 준비하지 못했습니다")
		return
	}
	path := filepath.Join(root, fileID)
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
	if err != nil {
		writeError(w, 500, "storage_error", "파일을 저장하지 못했습니다")
		return
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(out, hash), file)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		writeError(w, 500, "storage_error", "파일을 저장하지 못했습니다")
		return
	}
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(cleanName))
	}
	var id string
	err = a.db.QueryRow(r.Context(), `INSERT INTO documents(supplier_id,object_type,object_id,document_type,name,version,storage_path,content_type,size,checksum,expires_at,uploaded_by) VALUES(NULLIF($1,'')::uuid,NULLIF($2,''),NULLIF($3,'')::uuid,COALESCE(NULLIF($4,''),'other'),$5,COALESCE((SELECT max(version)+1 FROM documents WHERE supplier_id=NULLIF($1,'')::uuid AND document_type=COALESCE(NULLIF($4,''),'other') AND name=$5),1),$6,$7,$8,$9,NULLIF($10,'')::date,$11::uuid) RETURNING id`, r.FormValue("supplierId"), r.FormValue("objectType"), r.FormValue("objectId"), r.FormValue("documentType"), cleanName, path, contentType, size, hex.EncodeToString(hash.Sum(nil)), r.FormValue("expiresAt"), p.ID).Scan(&id)
	if err != nil {
		_ = os.Remove(path)
		writeError(w, 400, "save_failed", "문서 정보를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "upload", "document", id, nil, map[string]any{"name": cleanName, "size": size, "checksum": hex.EncodeToString(hash.Sum(nil)), "supplierId": r.FormValue("supplierId")})
	writeJSON(w, 201, map[string]any{"id": id, "name": cleanName, "size": size, "contentType": contentType, "checksum": hex.EncodeToString(hash.Sum(nil))})
}

func (a *App) portalDocuments(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.UserType != "supplier" || p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	query := r.URL.Query()
	query.Set("supplierId", *p.SupplierID)
	r.URL.RawQuery = query.Encode()
	a.listDocuments(w, r)
}

func (a *App) portalUploadDocument(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.UserType != "supplier" || p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 26<<20)
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		writeError(w, 400, "file_too_large", "파일은 25MB 이하여야 합니다")
		return
	}
	r.MultipartForm.Value["supplierId"] = []string{*p.SupplierID}
	a.uploadDocument(w, r)
}

func (a *App) listDocuments(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	supplierID := r.URL.Query().Get("supplierId")
	organizationID := ""
	principalSupplierID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	if p.SupplierID != nil {
		principalSupplierID = *p.SupplierID
	}
	limit := parseLimit(r, 200)
	rows, err := a.db.Query(r.Context(), `SELECT d.id,d.supplier_id,d.object_type,d.object_id,d.document_type,d.name,d.version,d.content_type,d.size,d.checksum,to_char(d.expires_at,'YYYY-MM-DD'),d.status,d.uploaded_by,d.created_at FROM documents d LEFT JOIN suppliers s ON s.id=d.supplier_id LEFT JOIN business_objects o ON o.id=d.object_id WHERE ($1='' OR d.supplier_id=$1::uuid) AND (($3='supplier' AND d.supplier_id=NULLIF($6,'')::uuid) OR ($3<>'supplier' AND (vendra_org_in_scope(s.organization_id,$4,NULLIF($5,'')::uuid) OR vendra_org_in_scope(o.organization_id,$4,NULLIF($5,'')::uuid) OR ($4='own' AND (s.owner_id=$7::uuid OR o.owner_id=$7::uuid OR d.uploaded_by=$7::uuid))))) ORDER BY d.created_at DESC LIMIT $2`, supplierID, limit+1, p.UserType, p.DataScope, organizationID, principalSupplierID, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "문서를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, typ, name, checksum, status string
		var linkedSupplierID, objectType, objectID, contentType, expires, uploader *string
		var version int
		var size int64
		var created any
		if rows.Scan(&id, &linkedSupplierID, &objectType, &objectID, &typ, &name, &version, &contentType, &size, &checksum, &expires, &status, &uploader, &created) == nil {
			items = append(items, map[string]any{"id": id, "supplierId": linkedSupplierID, "objectType": objectType, "objectId": objectID, "documentType": typ, "name": name, "version": version, "contentType": contentType, "size": size, "checksum": checksum, "expiresAt": expires, "status": status, "uploadedBy": uploader, "createdAt": created})
		}
	}
	items, truncated := truncate(items, limit)
	writeJSON(w, 200, map[string]any{"items": items, "limit": limit, "truncated": truncated})
}

func (a *App) downloadDocument(w http.ResponseWriter, r *http.Request) {
	a.serveDocument(w, r, false)
}

func (a *App) previewDocument(w http.ResponseWriter, r *http.Request) { a.serveDocument(w, r, true) }

func (a *App) serveDocument(w http.ResponseWriter, r *http.Request, inline bool) {
	if !a.documentAccessAllowed(r, r.PathValue("id")) {
		writeError(w, 403, "data_scope", "문서 접근 범위를 벗어났습니다")
		return
	}
	var path, name, contentType, checksum string
	var size int64
	err := a.db.QueryRow(r.Context(), `SELECT storage_path,name,COALESCE(content_type,'application/octet-stream'),size,checksum FROM documents WHERE id=$1 AND `+documentLive+``, r.PathValue("id")).Scan(&path, &name, &contentType, &size, &checksum)
	if err != nil {
		writeError(w, 404, "not_found", "문서를 찾을 수 없습니다")
		return
	}
	root, err := a.storagePath(r)
	if err != nil {
		writeError(w, 503, "storage_unavailable", "파일 저장소에 접근할 수 없습니다")
		return
	}
	absRoot, _ := filepath.Abs(root)
	absPath, _ := filepath.Abs(path)
	if !strings.HasPrefix(absPath, absRoot+string(os.PathSeparator)) {
		writeError(w, 403, "invalid_path", "파일 경로가 올바르지 않습니다")
		return
	}
	f, err := os.Open(absPath)
	if err != nil {
		writeError(w, 404, "file_missing", "저장된 파일을 찾을 수 없습니다")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", contentType)
	disposition := "attachment"
	if inline && (strings.HasPrefix(contentType, "image/") || contentType == "application/pdf" || strings.HasPrefix(contentType, "text/")) {
		disposition = "inline"
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'none'; sandbox")
	}
	w.Header().Set("Content-Disposition", contentDisposition(disposition, name))
	w.Header().Set("X-Content-SHA256", checksum)
	// Length comes from the file itself. Trusting the recorded size would
	// truncate or stall the response whenever the two disagree.
	if info, statErr := f.Stat(); statErr == nil {
		if info.Size() != size {
			slog.Warn("document size differs from the stored metadata", "document_id", r.PathValue("id"), "recorded", size, "on_disk", info.Size(), "request_id", requestID(r.Context()))
		}
		w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	}
	action := "download"
	if inline {
		action = "preview"
	}
	a.audit.record(r, action, "document", r.PathValue("id"), nil, map[string]any{"name": name})
	_, _ = io.Copy(w, f)
}

func (a *App) listDocumentSignatures(w http.ResponseWriter, r *http.Request) {
	if !a.documentAccessAllowed(r, r.PathValue("id")) {
		writeError(w, 403, "data_scope", "문서 접근 범위를 벗어났습니다")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT s.id,s.signer_id,u.display_name,u.email,s.signature_type,s.signature_metadata,s.signed_at FROM document_signatures s JOIN users u ON u.id=s.signer_id WHERE s.document_id=$1 ORDER BY s.signed_at`, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "database_error", "서명 이력을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, signer, name, email, typ string
		var metadata []byte
		var signed any
		if rows.Scan(&id, &signer, &name, &email, &typ, &metadata, &signed) == nil {
			var m any
			_ = json.Unmarshal(metadata, &m)
			items = append(items, map[string]any{"id": id, "signerId": signer, "signerName": name, "signerEmail": email, "signatureType": typ, "metadata": m, "signedAt": signed})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) signDocument(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if !a.documentAccessAllowed(r, r.PathValue("id")) {
		writeError(w, 403, "data_scope", "문서 접근 범위를 벗어났습니다")
		return
	}
	var in struct {
		SignatureType string `json:"signatureType"`
		Meaning       string `json:"meaning"`
		Comment       string `json:"comment"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if in.SignatureType == "" {
		in.SignatureType = "approval"
	}
	var checksum string
	if err := a.db.QueryRow(r.Context(), `SELECT checksum FROM documents WHERE id=$1 AND `+documentLive+``, r.PathValue("id")).Scan(&checksum); err != nil {
		writeError(w, 404, "not_found", "문서를 찾을 수 없습니다")
		return
	}
	metadata := map[string]any{"meaning": in.Meaning, "comment": in.Comment, "documentChecksum": checksum, "requestId": requestID(r.Context())}
	var id string
	err := a.db.QueryRow(r.Context(), `INSERT INTO document_signatures(document_id,signer_id,signature_type,signature_metadata) VALUES($1,$2,$3,$4) ON CONFLICT(document_id,signer_id,signature_type) DO UPDATE SET signature_metadata=excluded.signature_metadata,signed_at=now() RETURNING id`, r.PathValue("id"), p.ID, in.SignatureType, raw(metadata)).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "문서 서명을 저장하지 못했습니다")
		return
	}
	_, _ = a.db.Exec(r.Context(), `UPDATE documents SET status=CASE WHEN $2='approval' THEN 'approved' ELSE status END WHERE id=$1`, r.PathValue("id"), in.SignatureType)
	a.audit.record(r, "sign", "document", r.PathValue("id"), nil, metadata)
	writeJSON(w, 201, map[string]any{"id": id, "signedAt": time.Now(), "checksum": checksum})
}

func (a *App) documentAccessAllowed(r *http.Request, documentID string) bool {
	p, ok := principalFrom(r.Context())
	if !ok {
		return false
	}
	if grantAuthorized(r.Context()) {
		return true
	}
	organizationID := ""
	supplierID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	if p.SupplierID != nil {
		supplierID = *p.SupplierID
	}
	var allowed bool
	_ = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM documents d LEFT JOIN suppliers s ON s.id=d.supplier_id LEFT JOIN business_objects o ON o.id=d.object_id WHERE d.id=$1 AND (($2='supplier' AND d.supplier_id=NULLIF($5,'')::uuid) OR ($2<>'supplier' AND (vendra_org_in_scope(s.organization_id,$3,NULLIF($4,'')::uuid) OR vendra_org_in_scope(o.organization_id,$3,NULLIF($4,'')::uuid) OR ($3='own' AND (s.owner_id=$6::uuid OR o.owner_id=$6::uuid OR d.uploaded_by=$6::uuid))))))`, documentID, p.UserType, p.DataScope, organizationID, supplierID, p.ID).Scan(&allowed)
	return allowed
}
