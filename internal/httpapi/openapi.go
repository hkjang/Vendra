package httpapi

import "net/http"

func (a *App) openapi(w http.ResponseWriter, r *http.Request) {
	paths := map[string]any{
		"/api/v1/me":                                  map[string]any{"get": operation("Identity", "내 프로필 조회"), "patch": operation("Identity", "내 프로필 수정")},
		"/api/v1/suppliers":                           crudPath("Supplier", "공급업체 목록과 등록"),
		"/api/v1/suppliers/{id}":                      itemPath("Supplier", "Supplier 360 기본정보"),
		"/api/v1/suppliers/{id}/activity":             oneID("get", "Supplier", "공급업체 활동 및 감사 이력"),
		"/api/v1/suppliers/{id}/contacts":             map[string]any{"parameters": []any{idParameter()}, "get": operation("Supplier", "공급업체 담당자 조회"), "post": operation("Supplier", "공급업체 담당자 등록")},
		"/api/v1/suppliers/{id}/objects":              oneID("get", "Supplier", "Supplier 360 업무 객체 조회"),
		"/api/v1/contracts":                           crudPath("Contract", "계약"),
		"/api/v1/purchase-requests":                   crudPath("Procurement", "구매요청"),
		"/api/v1/rfq":                                 crudPath("Procurement", "견적요청"),
		"/api/v1/rfp":                                 crudPath("Procurement", "제안요청/입찰"),
		"/api/v1/purchase-orders":                     crudPath("Purchase", "발주"),
		"/api/v1/deliveries":                          crudPath("Purchase", "납품"),
		"/api/v1/inspections":                         crudPath("Quality", "검수"),
		"/api/v1/quality":                             crudPath("Quality", "품질/NCR/CAPA"),
		"/api/v1/issues":                              crudPath("Issue", "공급업체 이슈"),
		"/api/v1/invoices":                            crudPath("Finance", "Invoice"),
		"/api/v1/payments":                            crudPath("Finance", "지급"),
		"/api/v1/evaluations":                         map[string]any{"get": operation("Evaluation", "평가 목록")},
		"/api/v1/risks":                               map[string]any{"get": operation("Risk", "리스크 목록")},
		"/api/v1/spend":                               map[string]any{"get": operation("Analytics", "품목·조직·월·공급업체별 Spend 분석")},
		"/api/v1/spend/transactions":                  map[string]any{"post": operation("Analytics", "구매 원장 거래 적재")},
		"/api/v1/supplier-network":                    map[string]any{"get": operation("Risk", "N차 공급망 그래프")},
		"/api/v1/supplier-network/relationships":      map[string]any{"post": operation("Risk", "공급업체 간 관계 등록")},
		"/api/v1/documents":                           map[string]any{"get": operation("Document", "문서 목록")},
		"/api/v1/documents/upload":                    map[string]any{"post": operation("Document", "버전·체크섬 문서 업로드")},
		"/api/v1/documents/{id}/download":             oneID("get", "Document", "문서 다운로드 및 감사"),
		"/api/v1/documents/{id}/preview":              oneID("get", "Document", "문서 미리보기 및 감사"),
		"/api/v1/documents/{id}/signatures":           map[string]any{"parameters": []any{idParameter()}, "get": operation("Document", "전자서명 조회"), "post": operation("Document", "전자서명")},
		"/api/v1/suppliers/{id}/screenings":           map[string]any{"parameters": []any{idParameter()}, "get": operation("Screening", "공급업체 심사 이력"), "post": operation("Screening", "공급업체 심사 시작")},
		"/api/v1/screenings/{id}":                     map[string]any{"parameters": []any{idParameter()}, "patch": operation("Screening", "심사 응답 및 결과 갱신")},
		"/api/v1/screening-templates":                 map[string]any{"get": operation("Screening", "활성 심사 템플릿 조회")},
		"/api/v1/scorecards":                          map[string]any{"get": operation("Evaluation", "활성 Scorecard 조회")},
		"/api/v1/suppliers/{id}/evaluations":          map[string]any{"parameters": []any{idParameter()}, "get": operation("Evaluation", "공급업체 평가 이력"), "post": operation("Evaluation", "동적 Scorecard 평가")},
		"/api/v1/suppliers/{id}/risks":                map[string]any{"parameters": []any{idParameter()}, "get": operation("Risk", "공급업체 리스크"), "post": operation("Risk", "공급업체 리스크 등록")},
		"/api/v1/sourcing/{id}/participants":          map[string]any{"parameters": []any{idParameter()}, "get": operation("Sourcing", "참여업체 목록"), "post": operation("Sourcing", "복수 공급업체 초대")},
		"/api/v1/sourcing/{id}/comparison":            oneID("get", "Sourcing", "가격·품질·납기·위험·기술 종합 비교"),
		"/api/v1/sourcing/{id}/questions":             map[string]any{"parameters": []any{idParameter()}, "get": operation("Sourcing", "질의응답"), "post": operation("Sourcing", "질문·공지 등록")},
		"/api/v1/sourcing/{id}/committee":             map[string]any{"parameters": []any{idParameter()}, "get": operation("Sourcing", "평가위원 목록"), "post": operation("Sourcing", "평가위원 지정")},
		"/api/v1/sourcing-committee-candidates":       map[string]any{"get": operation("Sourcing", "평가위원 후보 조회")},
		"/api/v1/sourcing/responses/{id}/evaluate":    oneID("post", "Sourcing", "견적·제안 응답 평가"),
		"/api/v1/sourcing/questions/{id}/answer":      oneID("patch", "Sourcing", "참여업체 질의 답변"),
		"/api/v1/sourcing/{id}/select":                oneID("post", "Sourcing", "우선협상 또는 최종 업체 선정"),
		"/api/v1/workflows":                           crudPath("Workflow", "동적 승인 워크플로"),
		"/api/v1/workflows/{id}":                      oneID("patch", "Workflow", "승인 워크플로 수정"),
		"/api/v1/approvals":                           map[string]any{"get": operation("Workflow", "내 승인함")},
		"/api/v1/approvals/{id}/actions":              oneID("post", "Workflow", "승인·반려·보완 요청"),
		"/api/v1/dashboard":                           map[string]any{"get": operation("Analytics", "대시보드 KPI")},
		"/api/v1/search":                              map[string]any{"get": operation("Search", "권한이 적용된 통합 검색")},
		"/api/v1/ai/analyze":                          map[string]any{"post": operation("AI", "OpenAI 호환 공급업체 분석")},
		"/api/v1/ai/contracts/{id}/analyze":           oneID("post", "AI", "계약 조건 추출 및 위험조항 분석"),
		"/api/v1/me/api-keys":                         map[string]any{"get": operation("Identity", "개인 API Key 목록"), "post": operation("Identity", "개인 API Key 생성")},
		"/api/v1/me/api-keys/{id}":                    oneID("delete", "Identity", "개인 API Key 폐기"),
		"/api/v1/me/api-keys/{id}/rotate":             oneID("post", "Identity", "개인 API Key 회전"),
		"/api/v1/me/notifications":                    map[string]any{"get": operation("Notification", "내 알림 조회")},
		"/api/v1/me/notifications/read-all":           map[string]any{"post": operation("Notification", "모든 알림 읽음 처리")},
		"/api/v1/me/notifications/{id}/read":          oneID("post", "Notification", "알림 읽음 처리"),
		"/api/v1/admin/settings":                      map[string]any{"get": operation("Administration", "서비스 설정 조회")},
		"/api/v1/admin/settings/{key}":                keyedPath("key", "put", "Administration", "서비스 설정 저장"),
		"/api/v1/admin/users":                         crudPath("Administration", "사용자"),
		"/api/v1/admin/users/{id}":                    oneID("patch", "Administration", "사용자 수정"),
		"/api/v1/admin/roles":                         crudPath("Administration", "역할과 권한"),
		"/api/v1/admin/roles/{id}":                    oneID("patch", "Administration", "역할과 권한 수정"),
		"/api/v1/admin/organizations":                 crudPath("Administration", "조직"),
		"/api/v1/admin/access-grants":                 crudPath("Administration", "조건부·임시 권한과 위임"),
		"/api/v1/admin/access-grants/{id}":            oneID("delete", "Administration", "조건부·임시 권한 회수"),
		"/api/v1/admin/lifecycle":                     map[string]any{"get": operation("Administration", "Lifecycle 상태 조회")},
		"/api/v1/admin/lifecycle/{entityType}":        keyedPath("entityType", "put", "Administration", "Lifecycle 상태 저장"),
		"/api/v1/admin/audit":                         map[string]any{"get": operation("Audit", "감사로그 조회")},
		"/api/v1/admin/logs":                          map[string]any{"get": operation("Administration", "현재 서버 런타임 로그 조회")},
		"/api/v1/admin/scorecards":                    crudPath("Administration", "Scorecard 템플릿"),
		"/api/v1/admin/screening-templates":           crudPath("Administration", "심사 템플릿"),
		"/api/v1/portal/profile":                      map[string]any{"get": operation("Portal", "공급업체 회사정보"), "patch": operation("Portal", "공급업체 연락정보 수정")},
		"/api/v1/portal/contacts":                     map[string]any{"get": operation("Portal", "공급업체 담당자 조회"), "post": operation("Portal", "공급업체 담당자 등록")},
		"/api/v1/portal/contacts/{id}/verification":   oneID("post", "Portal", "담당자 이메일 인증 요청"),
		"/api/v1/portal/work":                         map[string]any{"get": operation("Portal", "계약·발주·납품·Invoice 업무 조회")},
		"/api/v1/portal/evaluations":                  map[string]any{"get": operation("Portal", "평가 결과와 개선 요청 조회")},
		"/api/v1/portal/inquiries":                    map[string]any{"post": operation("Portal", "공급업체 문의 등록")},
		"/api/v1/portal/documents":                    map[string]any{"get": operation("Portal", "공급업체 문서 조회")},
		"/api/v1/portal/documents/upload":             map[string]any{"post": operation("Portal", "공급업체 문서 제출")},
		"/api/v1/portal/sourcing":                     map[string]any{"get": operation("Portal", "초대받은 RFQ/RFP")},
		"/api/v1/portal/sourcing/{id}/response":       keyedPath("id", "put", "Portal", "견적·제안 응답 저장 및 제출"),
		"/api/v1/portal/sourcing/{id}/decline":        oneID("post", "Portal", "RFQ/RFP 참여 거절"),
		"/api/v1/portal/sourcing/{id}/questions":      map[string]any{"parameters": []any{idParameter()}, "get": operation("Portal", "RFQ/RFP 질의 조회"), "post": operation("Portal", "RFQ/RFP 질의 등록")},
		"/api/v1/portal/purchase-orders/{id}/confirm": oneID("post", "Portal", "발주 확인"),
		"/api/v1/portal/contracts/{id}/confirm":       oneID("post", "Portal", "계약 확인"),
		"/api/v1/portal/deliveries":                   map[string]any{"post": operation("Portal", "납품 등록")},
		"/api/v1/portal/invoices":                     map[string]any{"post": operation("Portal", "Invoice 등록")},
		"/api/v1/invitations":                         map[string]any{"post": operation("Supplier", "Self Registration 초대 발급")},
		"/api/v1/openapi.json":                        map[string]any{"get": operation("Integration", "OpenAPI 3.1 명세 조회")},
	}
	for _, route := range objectRoutes {
		paths[route.path+"/{id}"] = map[string]any{
			"parameters": []any{idParameter()},
			"get":        operation("Business Object", route.objectType+" 상세 조회"),
			"patch":      operation("Business Object", route.objectType+" 수정"),
		}
		paths[route.path+"/{id}/submit"] = oneID("post", "Workflow", route.objectType+" 제출")
	}
	writeJSON(w, 200, map[string]any{
		"openapi":  "3.1.0",
		"info":     map[string]any{"title": "Vendra API", "version": Version, "description": "Enterprise Supplier Intelligence Platform REST API"},
		"servers":  []map[string]string{{"url": "/", "description": "현재 Vendra 서버"}},
		"security": []map[string]any{{"session": []string{}}, {"bearerAuth": []string{}}},
		"paths":    paths,
		"components": map[string]any{"securitySchemes": map[string]any{
			"session":    map[string]any{"type": "apiKey", "in": "cookie", "name": sessionCookie},
			"bearerAuth": map[string]any{"type": "http", "scheme": "bearer", "description": "개인화 페이지에서 발급한 vnd_ API key"},
		}, "schemas": map[string]any{
			"Supplier":       map[string]any{"type": "object", "required": []string{"id", "supplierNumber", "name", "businessNumber", "status", "riskLevel"}, "properties": map[string]any{"id": str("uuid"), "supplierNumber": str(""), "name": str(""), "businessNumber": str(""), "status": str(""), "grade": str(""), "riskLevel": str(""), "annualSpend": map[string]any{"type": "number"}, "score": map[string]any{"type": "number"}}},
			"BusinessObject": map[string]any{"type": "object", "required": []string{"id", "objectType", "number", "title", "status"}, "properties": map[string]any{"id": str("uuid"), "objectType": str(""), "number": str(""), "supplierId": str("uuid"), "title": str(""), "status": str(""), "amount": map[string]any{"type": "number"}, "data": map[string]any{"type": "object", "additionalProperties": true}}},
		}},
	})
}

func operation(tag, summary string) map[string]any {
	return map[string]any{"tags": []string{tag}, "summary": summary, "responses": map[string]any{"200": map[string]any{"description": "Success"}, "400": map[string]any{"description": "Invalid request"}, "401": map[string]any{"description": "Unauthenticated"}, "403": map[string]any{"description": "Forbidden"}}}
}
func crudPath(tag, summary string) map[string]any {
	return map[string]any{"get": operation(tag, summary+" 조회"), "post": operation(tag, summary+" 생성")}
}
func itemPath(tag, summary string) map[string]any {
	return map[string]any{"parameters": []any{idParameter()}, "get": operation(tag, summary+" 조회"), "patch": operation(tag, summary+" 수정")}
}
func idParameter() map[string]any {
	return map[string]any{"name": "id", "in": "path", "required": true, "schema": str("uuid")}
}
func oneID(method, tag, summary string) map[string]any {
	return map[string]any{"parameters": []any{idParameter()}, method: operation(tag, summary)}
}
func keyedPath(name, method, tag, summary string) map[string]any {
	return map[string]any{"parameters": []any{map[string]any{"name": name, "in": "path", "required": true, "schema": str("")}}, method: operation(tag, summary)}
}
func str(format string) map[string]any {
	m := map[string]any{"type": "string"}
	if format != "" {
		m["format"] = format
	}
	return m
}
