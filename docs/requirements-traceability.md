# Requirements traceability

이 문서는 최초 SRM 개발 요건의 1~47절과 Vendra 구현을 연결한다. 상태는 현재 릴리스 기준이며, 상세 API는 로그인 후 `/api/v1/openapi.json`에서 확인한다.

| 절 | 구현 | 주요 근거 |
|---:|---|---|
| 1 | Enterprise Supplier Intelligence Platform | Supplier부터 퇴출까지 단일 PostgreSQL 모델과 Supplier 360 UI |
| 2 | 핵심 목표 전체 | Dashboard, lifecycle, sourcing, contract, quality, evaluation, risk, spend, audit, AI |
| 3 | 사용자·권한 | RBAC/ABAC, own/department/division/company scope, 필드 마스킹, 승인권한, 위임·임시권한 |
| 4 | Supplier Master | 법적·기본·주소·연락·업종·재무·거래·계좌·세금·ERP 정보와 암호화 저장 |
| 5 | Lifecycle | 관리자 lifecycle editor와 entity별 상태 정의 |
| 6 | 내부/Self Registration | 내부 등록, 1회성 초대 URL, 중복 사업자번호·유사업체·필수문서·이메일·계좌변경 검증 |
| 7 | 공급업체 심사 | 관리자 심사 템플릿, 영역별 점수, 필수문서, 4단계 결과 엔진 |
| 8 | Dynamic Scorecard | 관리자 기준/가중치/등급 규칙과 자동 총점·등급 |
| 9 | 평가 유형·360도 평가 | 유형/기간/평가자별 평가 레코드와 완료 평가 집계 |
| 10 | 구매요청 | 목적·품목·수량·금액·예산·희망일·추천업체·문서 모델, 제출 workflow |
| 11 | RFQ | 생성, 복수 초대, Portal 견적, 마감 강제, 비교, 위원 평가, 우선협상 |
| 12 | RFP/입찰 | RFP 템플릿 설정, 초대/Q&A/제안/위원/기술·가격 평가/선정 |
| 13 | 견적 비교 | 가격·품질·납기·위험·기술 가중 종합점수 Matrix |
| 14 | 계약 관리 | 유형·금액·기간·갱신·SLA·지급조건·보증·책임자 |
| 15 | 계약 Lifecycle | 초안부터 종료까지 관리자 편집 가능한 기본 상태 |
| 16 | 계약 자동 알림 | 계약/문서 만료, SLA, 계약 초과, 평가 알림과 log/webhook/Slack/Mattermost/email/SMS adapter |
| 17 | 발주 | PO 생성·승인·Portal 확인·납품·검수·Invoice·Payment 연계 |
| 18 | 납품·검수 | Portal 납품, 내부 inspection 및 부모 업무 연결 |
| 19 | 품질 | inspection/quality 객체의 defect, NCR, RCA, CAPA, 조치계획, score |
| 20 | Issue | 유형·심각도·담당자·발생일·조치·기한·상태·RCA·CAPA |
| 21 | Risk | 유형별 probability×impact 점수, severity, 완화조치, 공급업체 최고위험 반영 |
| 22 | Risk Heatmap | 실제 risk API 데이터 기반 probability/impact 사분면 UI |
| 23 | Supplier 360 | Overview 및 Contacts/Contracts/PO/Deliveries/Quality/Evaluations/Risks/Issues/Documents/Spend/Activity/Audit 탭 |
| 24 | Spend Analysis | 공급업체·품목·조직·월, 계약/비계약, Top Supplier, 의존도 집계 |
| 25 | 의존도 | 구매 비중, 대체 관계, 품목, 위험 단계 산정 |
| 26 | Supplier Portal | 별도 Portal 권한 경계와 회사/담당자/문서/RFQ/RFP/계약/PO/납품/Invoice/평가/문의 기능 |
| 27 | 문서 관리 | 버전, 만료, 상태, 서명, SHA-256, preview/download 및 접근 감사 |
| 28 | Workflow Engine | 객체·금액·조직·계약유형·Risk·품목·프로젝트·보안 조건과 순차 단계; 전체 비활성 시 자동 승인 |
| 29 | Dashboard | 공급업체/구매/계약/위험/평가/납기/품질/승인·심사·갱신 KPI |
| 30 | Network Graph | N차 supplier relationship graph, 중요도·의존율·연쇄 위험 UI |
| 31 | AI Supplier Analyst | 관리자 OpenAI-compatible gateway, 근거 데이터 기반 요약·비교·위험·Spend·추천·자연어 질의 |
| 32 | AI 계약 분석 | 조건 추출, risk clause, 법무검토 필요 표시와 결과 저장 |
| 33 | Recommendation | 품목·거래·가격·납기·품질·Risk·평가·규모 기반 MCP 추천 |
| 34 | 관리자 설정 | 등급/Risk/평가/유형/품목/계약/workflow/알림/문서/인증/SLA/연동/API Key/AI 정책 |
| 35 | Audit Log | actor/time/action/object/before/after/IP/session/request ID, 민감필드 읽기와 CSV 내보내기 |
| 36 | 통합 검색 | Supplier/Contact/Contract/PO/RFQ/RFP/PR/Document/Issue/Evaluation 및 서버 권한 필터 |
| 37 | REST/OpenAPI | `/api/v1`, OpenAPI 3.1 JSON, session/API-key 인증 정의 |
| 38 | MCP | 11개 조회 도구, REST와 동일한 permission/data scope/field masking; 변경 도구 미노출 |
| 39 | 외부 연계 | 관리자 설정형 OIDC, OpenAI gateway, notification/webhook 및 REST API |
| 40 | 기술 아키텍처 | React+TypeScript, Go, PostgreSQL FTS/Job, filesystem, Docker, JSON log, Prometheus metrics |
| 41 | 논리 아키텍처 | 내부 UI/Portal/MCP → 단일 API/domain service → PostgreSQL/AI/integration |
| 42 | Domain | Identity부터 Audit까지 모듈형 Go handler와 전용/공통 DB 모델 |
| 43 | 데이터 모델 | supplier 관계와 sourcing/contract/PO/delivery/evaluation/risk/document FK 모델 |
| 44 | UI 메뉴 | 업무 중심 Supplier/Procurement/Contract/Purchase/Quality/Analytics/AI/Admin 메뉴 |
| 45 | UX 원칙 | Supplier 360 상단에 grade/risk/spend/contracts/performance/issues를 즉시 노출 |
| 46 | 개발 범위 | Phase 1~3에 명시된 모든 domain을 단일 릴리스에 포함 |
| 47 | 차별화 기능 | Supplier 360, graph, scorecard, workflow, spend, AI, recommendation, Portal, MCP/API |

## 배포 제약 추적

- 환경변수는 `POSTGRES_DSN`, `BOOTSTRAP_ADMIN`, `BOOTSTRAP_ADMIN_PASSWORD`, `ENCRYPTION_KEY` 네 개만 읽는다.
- Keycloak OIDC, AI, storage, notification, workflow와 운영 정책은 DB-backed 관리자 설정이다.
- 로그인/가입 및 프로필 메뉴에 빌드 버전이 표시된다.
- 개인화 화면에서 최소 scope, 만료, 폐기, 회전을 지원하고 현재 역할 권한을 항상 상한으로 적용한다.
- `vX.Y.Z` tag workflow는 `vendra:vX.Y.Z` 이미지를 만들고 `vendra-vX.Y.Z.tar.gz` 하나만 GitHub Release에 첨부한다.

## 릴리스 검증

```bash
go test ./cmd/... ./internal/...
go vet ./cmd/... ./internal/...
npm run build --prefix web
npm run lint --prefix web
sh scripts/offline-release.sh 0.2.0
gzip -t dist/vendra-v0.2.0.tar.gz
```
