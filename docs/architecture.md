# Vendra architecture

## Runtime topology

```text
Internal user ─┐
Supplier user ─┼─ HTTPS ─ Vendra (React + REST + MCP + Jobs) ─ PostgreSQL
AI Agent ──────┘                         │
                                        └─ /var/lib/vendra/documents
```

Vendra는 모듈형 모놀리스입니다. 도메인 경계는 Identity, Organization, Supplier, Procurement, Contract, Purchase, Quality, Evaluation, Risk, Issue, Document, Spend, Workflow, Notification, Integration, AI, Audit로 나뉘지만 단일 트랜잭션과 단일 배포 단위를 유지합니다.

`business_objects`는 계약, 구매요청, RFQ/RFP, 발주, 납품, 검수, 품질, 이슈와 Invoice의 공통 수명주기·금액·담당·조직 필드를 보관하고 각 도메인의 가변 필드는 JSONB `data`에 둡니다. 공급업체, 평가, 리스크, 문서, Workflow, 권한과 감사로그는 강한 무결성이 필요한 전용 테이블입니다.

## Request flow

1. 세션 cookie 또는 개인 API key를 SHA-256 hash로 확인합니다.
2. 역할과 유효기간 내 임시 권한을 합쳐 Principal을 만듭니다.
3. Route permission, 조직/소유자 Data Scope, 공급업체 포털 scope와 필드 권한을 순서대로 적용합니다.
4. Domain handler가 PostgreSQL transaction을 수행합니다.
5. 변경 전후 값, actor, IP, session, request ID를 감사로그에 남깁니다.

## Workflow

Workflow 정의는 object type, 조건 JSON과 순서가 있는 step JSON으로 버전 관리됩니다. 제출 시 조건을 현재 객체와 대조해 첫 일치 Workflow를 선택합니다. 전체 승인 설정이 꺼져 있거나 일치 정의가 없으면 승인 인스턴스를 만들지 않고 자동 승인합니다.

## Extension points

- Notification adapter: `log`, generic webhook, Slack, Mattermost
- AI gateway: OpenAI-compatible `/chat/completions`
- Identity: local bootstrap authentication and Keycloak-compatible OIDC discovery
- Storage: 현재 filesystem, 설정 모델은 S3-compatible driver 확장을 고려
- Search/Queue: PostgreSQL FTS와 `SKIP LOCKED` 확장 가능한 DB queue
