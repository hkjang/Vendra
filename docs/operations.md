# Operations

## Health and logs

- `/health/live`: process liveness
- `/health/ready`: PostgreSQL readiness
- `/metrics`: Prometheus exposition (build, HTTP 요청/오류/지연, 로그인 실패/잠금, PostgreSQL pool)
- stdout: structured JSON request/application logs with request ID
- 관리자 UI: `서비스 관리 → 서버 로그`에서 현재 프로세스의 최근 로그 검색, 레벨 필터, 자동 갱신, 속성 확인과 JSON 내보내기
- Docker `HEALTHCHECK`: readiness endpoint every 30 seconds

관리자 로그 화면은 프로세스 메모리에 최근 3,000건을 순환 보관하며 관리자(`*`) 권한만 접근할 수 있습니다. 비밀번호, 토큰, 쿠키, 인증 헤더, 요청 본문, DSN과 암호화 키 계열 속성은 수집 시 마스킹됩니다. 이 화면은 장애 현황의 빠른 확인용이며 영구 로그 저장소가 아닙니다. 장기 보관과 여러 인스턴스의 통합 조회는 stdout JSON 로그를 Docker 로그 드라이버, Loki, OpenSearch 또는 조직 표준 수집기로 전달하세요.

Request ID로 한 요청의 흐름을 추적할 수 있습니다. UI 검색창에는 Request ID, 경로, 메시지 또는 구조화 속성 값을 입력할 수 있고, API로는 `GET /api/v1/admin/logs?level=ERROR&query=<request-id>&limit=300`을 사용합니다. API 응답은 현재 프로세스의 로그만 포함하며 최대 조회량은 500건입니다.

## Database migration

Embedded, ordered SQL migrations run inside a PostgreSQL transaction at startup. `schema_migrations` prevents reapplication. 기동 시 PostgreSQL advisory lock을 잡으므로 여러 인스턴스가 동시에 올라와도 마이그레이션은 한 번에 하나씩 적용되고, 나머지 인스턴스는 완료를 기다린 뒤 이미 적용된 버전을 건너뜁니다. Back up before upgrading and never run two different Vendra versions against the same schema during migration. 데이터가 있는 구버전 스키마에서 최신까지 올리는 경로는 통합 테스트로 검증됩니다 — 누적된 중복 승인 정리, 기존 데이터 보존, 신규 설정 시드, 재실행 시 무해함까지 확인합니다.

## Notification adapters

`notification.adapters`는 어댑터 배열입니다. 각 항목은 `name`, `type`(`log`, `slack`, `mattermost`, `webhook`, `email`, `sms`, `internal_messenger`), `url`, `enabled`을 가지며 `timeoutSeconds`로 호출 상한을 정합니다. 생략하면 10초이고 1~120초로 제한됩니다.

```json
[{ "name": "ops-slack", "type": "slack", "url": "https://...", "enabled": true, "timeoutSeconds": 10 }]
```

배경 작업은 매시간 최대 50건을 순차 전송하며, 응답하지 않는 어댑터는 설정된 시간에 끊고 다음 건으로 넘어갑니다. 5회 실패한 전송은 재시도하지 않습니다. 한 번의 배경 작업 전체에도 30분 상한이 있어 어떤 통합이나 질의도 매시간 반복 실행을 영구히 멈출 수 없습니다.

## Performance

읽기 경로는 공급업체 5천, 업무 5만, 지출 10만, 문서 2만, 감사로그 20만 건 규모에서 측정합니다. 대시보드와 업무 관제탑은 범위 안 업무를 집계하므로 데이터 양에 비례합니다(위 규모에서 약 110ms). 목록 조회는 인덱스로 상수 시간에 가깝습니다.

```bash
VENDRA_PERF=1 VENDRA_TEST_DSN="postgres://..." go test ./internal/httpapi/ -run TestMeasureEndpointLatency -v
```

## Retention

만료된 운영 데이터는 시간당 백그라운드 스윕이 정리합니다. `maintenance.retention` 설정으로 조정하며 `0`은 해당 스윕을 끕니다.

| 키 | 기본값 | 대상 |
|---|---|---|
| `expiredSessionDays` | 7 | 만료 시각이 지난 `sessions` 행 |
| `loginAttemptDays` | 30 | `login_attempts` 로그인 시도 이력 |
| `formDraftDays` | 60 | 제출되지 않고 방치된 입력 자동 저장 |

만료된 세션은 이미 인증에 사용할 수 없으므로 삭제해도 동작이 바뀌지 않습니다. 업무 데이터, 감사로그와 알림은 이 스윕이 건드리지 않습니다. 임시저장은 사용자당 최근 50건만 유지되며, 정상 등록 시에는 즉시 삭제됩니다.

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
