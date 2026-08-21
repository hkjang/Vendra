# Operations

## Health and logs

- `/health/live`: process liveness
- `/health/ready`: PostgreSQL readiness
- `/metrics`: Prometheus exposition (build, HTTP 요청/오류/지연, PostgreSQL pool)
- stdout: structured JSON request/application logs with request ID
- 관리자 UI: `서비스 관리 → 서버 로그`에서 현재 프로세스의 최근 로그 검색, 레벨 필터, 자동 갱신, 속성 확인과 JSON 내보내기
- Docker `HEALTHCHECK`: readiness endpoint every 30 seconds

관리자 로그 화면은 프로세스 메모리에 최근 3,000건을 순환 보관하며 관리자(`*`) 권한만 접근할 수 있습니다. 비밀번호, 토큰, 쿠키, 인증 헤더, 요청 본문, DSN과 암호화 키 계열 속성은 수집 시 마스킹됩니다. 이 화면은 장애 현황의 빠른 확인용이며 영구 로그 저장소가 아닙니다. 장기 보관과 여러 인스턴스의 통합 조회는 stdout JSON 로그를 Docker 로그 드라이버, Loki, OpenSearch 또는 조직 표준 수집기로 전달하세요.

Request ID로 한 요청의 흐름을 추적할 수 있습니다. UI 검색창에는 Request ID, 경로, 메시지 또는 구조화 속성 값을 입력할 수 있고, API로는 `GET /api/v1/admin/logs?level=ERROR&query=<request-id>&limit=300`을 사용합니다. API 응답은 현재 프로세스의 로그만 포함하며 최대 조회량은 500건입니다.

## Database migration

Embedded, ordered SQL migrations run inside a PostgreSQL transaction at startup. `schema_migrations` prevents reapplication. Back up before upgrading and never run two different Vendra versions against the same schema during migration.

## Productivity data

업무 관제탑의 확인·보류 상태, 저장된 보기와 입력 자동 저장 내용은 현재 로그인 사용자에 귀속되어 PostgreSQL에 저장됩니다. 기한이 변경되면 새 업무 신호가 생성되며, 자동 저장 초안은 해당 업무를 정상 등록할 때 삭제됩니다. 이 데이터도 일반 업무 데이터와 함께 PostgreSQL 백업에 포함됩니다.

## Release and rollback

Load the target archive and update the exact tag in `compose.yaml`. For rollback, restore the matching PostgreSQL/document backup if a newer migration is not backward compatible.

```bash
docker load < vendra-v0.3.0.tar.gz
docker compose up -d
docker compose ps
curl -fsS http://localhost:8080/health/ready
```

## Offline prerequisites

Transfer the Vendra archive and, if the target network does not already have them, an approved PostgreSQL image or PostgreSQL installation media. Vendra itself makes no required internet calls. OIDC, webhook and AI endpoints may point to services inside the offline network.
