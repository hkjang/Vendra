<p align="center">
  <img src="docs/favicon.svg" alt="Vendra Logo" width="90"><br><br>
  <h1 align="center">Vendra</h1>
</p>

<p align="center">
  <strong>오프라인 대응 엔터프라이즈 공급업체 통합 SRM & 인텔리전스 플랫폼</strong><br>
  Supplier 360, 구매 수명주기, 동적 스코어카드, 포털 보안 격리 및 11+ Read-Only MCP 지원.
</p>

<p align="center">
  <a href="https://hkjang.github.io/Vendra/">🇰🇷 홍보 페이지</a> · <a href="https://hkjang.github.io/Vendra/index_en.html">🇺🇸 English Page</a> · <a href="https://github.com/sponsors/hkjang">💖 Sponsor</a>
</p>

---

단일 Go 프로세스가 React UI, REST/OpenAPI API, MCP 서버와 백그라운드 작업을 제공하며 PostgreSQL 외에 Redis, Kafka, Elasticsearch 같은 필수 미들웨어가 없습니다. 런타임은 외부 인터넷 연결 없이 동작합니다.

## 주요 기능

- Supplier 360: 기본정보, 담당자, 계약, PO, 납품, 품질, 평가, 리스크, 이슈, 문서, Spend, 활동과 감사로그
- 구매 수명주기: 구매요청, RFQ, RFP, 계약, 발주, 납품, 검수, Invoice
- Dynamic Scorecard: 관리자 정의 항목·가중치·등급 기준과 자동 점수/등급 산정
- Workflow Engine: 금액, 조직, 계약유형, Risk, 품목, 프로젝트, 보안등급 조건과 다단계 승인
- 선택적 승인: 관리자가 전체 승인 프로세스를 끄면 검토·승인·반려 단계 없이 제출 즉시 승인
- IAM: RBAC, 조직/소유자 Data Scope, 필드 권한, 임시 권한·위임, 공급업체 포털 격리
- Keycloak OIDC: 관리자 화면에서 Issuer, Client ID, Client Secret만 설정하는 Discovery + Authorization Code + PKCE 연동
- 개인 키 관리: 개인 API 키 생성, 최소 Scope, 만료, 즉시 폐기 및 원클릭 회전
- 문서: 파일시스템 저장, 버전, 만료일, SHA-256 Checksum, 다운로드 감사 추적
- Intelligence: Spend 집중도, 공급망 Network, 공급업체 추천, OpenAI 호환 AI Analyst
- Integration: REST/OpenAPI 3.1, 조회 전용 MCP 도구, PostgreSQL Job/Notification Adapter
- 운영: 자동 DB migration, JSON log, liveness/readiness, 비-root/read-only Docker 실행

## 빠른 시작

요구사항은 PostgreSQL 15 이상과 Docker입니다. PostgreSQL 데이터베이스와 사용자를 먼저 만들고 네 개 값만 준비합니다.

```bash
cp .env.example .env
openssl rand -base64 32  # 출력값을 ENCRYPTION_KEY에 입력
docker load < vendra-v0.1.0.tar.gz
docker compose up -d
```

접속 주소는 `http://localhost:8080`입니다. 첫 실행 시 `BOOTSTRAP_ADMIN` 계정이 시스템 관리자로 생성됩니다. 환경변수 비밀번호는 최초 생성에만 사용하며 기존 계정 비밀번호를 재시작 때 덮어쓰지 않습니다.

애플리케이션이 읽는 환경변수는 다음 네 개뿐입니다.

| 이름 | 설명 |
|---|---|
| `POSTGRES_DSN` | PostgreSQL 연결 문자열. 운영에서는 TLS(`sslmode=require` 이상)를 권장합니다. |
| `BOOTSTRAP_ADMIN` | 최초 시스템 관리자 이메일 |
| `BOOTSTRAP_ADMIN_PASSWORD` | 최초 시스템 관리자 비밀번호 |
| `ENCRYPTION_KEY` | Secret과 계좌정보 암호화용 base64 인코딩 32-byte 키 |

OIDC, AI, 저장소, 알림, Workflow, 평가, Risk와 모든 운영 정책은 로그인 후 **서비스 관리**에서 저장합니다. Secret은 `ENCRYPTION_KEY`로 AES-256-GCM 암호화되어 PostgreSQL에 저장됩니다.

## 개발

```bash
go test ./cmd/... ./internal/...
cd web && npm ci && npm run build
go run ./cmd/vendra
```

프론트 개발 서버가 필요하면 별도 터미널에서 `cd web && npm run dev`를 실행합니다. `/api`와 `/mcp`는 로컬 `:8080`으로 프록시됩니다.

## API와 MCP

- OpenAPI: `GET /api/v1/openapi.json`
- REST base: `/api/v1`
- MCP Streamable HTTP endpoint: `POST /mcp`
- 인증: HttpOnly 로그인 세션 또는 개인화 화면에서 만든 `Authorization: Bearer vnd_...` API 키

MCP는 `search_suppliers`, `get_supplier`, `compare_suppliers`, `get_supplier_risk`, `get_supplier_score`, `search_contracts`, `get_expiring_contracts`, `analyze_spend`, `search_purchase_orders`, `get_supplier_issues`, `recommend_suppliers` 조회 도구를 제공합니다. 변경 도구는 노출하지 않아 AI Agent의 조회 권한과 서비스의 승인 대상 변경 권한을 명확히 분리합니다.

## 오프라인 릴리스

Git tag `vX.Y.Z`를 push하면 GitHub Actions가 다음 규칙을 강제합니다.

- Docker image: `vendra:vX.Y.Z`
- GitHub Release 파일: `vendra-vX.Y.Z.tar.gz`
- Release에는 서비스 Docker image archive 하나만 첨부

로컬에서도 같은 결과를 만들 수 있습니다.

```bash
sh scripts/offline-release.sh 0.1.0
docker load < dist/vendra-v0.1.0.tar.gz
```

이미지는 UI 정적 파일, timezone/CA 인증서와 Go 서버를 포함합니다. 실행 중 CDN이나 외부 패키지 저장소를 사용하지 않습니다. Keycloak, AI, webhook 등 선택 연동은 관리자 설정에서 비활성 상태가 기본입니다.

## 백업과 복구

일관된 복구를 위해 같은 시점의 PostgreSQL과 문서 volume을 함께 백업합니다.

```bash
pg_dump --format=custom --dbname "$POSTGRES_DSN" --file vendra.dump
docker run --rm -v vendra_vendra_documents:/data -v "$PWD":/backup alpine tar -czf /backup/vendra-documents.tar.gz -C /data .
```

`ENCRYPTION_KEY`는 DB와 별도의 안전한 비밀 저장소에 백업해야 합니다. 키를 분실하면 암호화된 OIDC/AI Secret과 계좌정보를 복구할 수 없습니다.

## 보안 운영 메모

- TLS 종료 프록시 뒤에서 운영하고 `security.session.secureCookie`를 켭니다.
- Bootstrap 계정으로 조직/역할을 구성한 후 일상 관리용 별도 계정을 사용합니다.
- API 키는 용도별 최소 Scope와 만료일을 지정하고 정기 회전합니다.
- 계좌, 계약금액, 평가, Risk, 승인, 권한과 문서 접근 이벤트는 감사로그에서 확인합니다.
- 공급업체 포털 계정은 자신의 `supplier_id` 데이터만 조회하도록 서버에서 강제됩니다.

상세 설계와 운영 점검 항목은 [docs/architecture.md](docs/architecture.md), [docs/security.md](docs/security.md), [docs/operations.md](docs/operations.md), [docs/requirements-traceability.md](docs/requirements-traceability.md)를 참고하세요.
