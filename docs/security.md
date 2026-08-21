# Security model

## Authentication

- Local password: bcrypt(DefaultCost). 본인은 `POST /api/v1/me/password`로, 관리자는 `POST /api/v1/admin/users/{id}/password`로 교체합니다. 두 경로 모두 대상 사용자의 세션을 폐기하며(본인 변경은 요청한 세션만 유지) 실패한 로그인 이력도 함께 지웁니다.
- OIDC: Discovery, Authorization Code, PKCE S256, state, nonce, issuer/audience/signature verification
- OIDC 계정 연결: 이미 연결된 `oidc_subject`는 그 자체로 계정을 특정하므로 이메일 검증이 필요 없습니다. 반면 **이메일로 기존 계정에 연결하거나 새 계정을 만드는 경로**는 공급자의 이메일 클레임을 신뢰하는 지점이므로 `email_verified`가 참이어야 합니다. 사용자가 이메일을 임의로 설정할 수 있는 공급자에서 관리자 계정을 포함한 기존 계정을 가로채는 것을 막습니다. `oidc.requireVerifiedEmail` 기본값은 켜짐이며, 해당 클레임을 보내지 않는 공급자에 한해 관리자 화면에서 끌 수 있습니다. 거부는 `oidc_unverified_email` 감사 이벤트로 남습니다.
- Session: random 256-bit bearer, database에는 SHA-256 hash만 저장, HttpOnly/SameSite cookie
- API key: `vnd_` prefix, 생성 시 한 번만 표시, hash 저장, Scope/만료/폐기/회전
- Brute force protection: `login_attempts`에 실패 이력을 남기고 계정과 발신 IP 각각에 임계값을 적용합니다. 잠긴 동안에는 자격증명을 확인하지 않고 `429 too_many_attempts`와 `Retry-After`를 반환하며, 로그인에 성공하면 해당 계정의 실패 이력을 지웁니다.
- Account enumeration: 존재하지 않는 계정에도 동일 cost의 bcrypt 비교를 수행하므로 응답 시간과 응답 본문 모두 계정 존재 여부를 드러내지 않습니다.

### `security.password`

| 키 | 기본값 | 설명 |
|---|---|---|
| `minLength` | 10 | 최소 **문자 수**. 8~64 범위를 벗어나면 기본값으로 되돌아갑니다. |
| `requireClasses` | 0 | 영문 소문자·대문자·숫자·특수문자 중 필요한 종류 수. `0`이면 제한하지 않습니다. |

길이는 사람이 세는 방식대로 문자 단위로 검사하고, 상한은 bcrypt가 제한하는 72**바이트**로 검사합니다. 한글은 한 글자가 3바이트이므로 24자가 상한입니다. 같은 정책이 관리자 사용자 생성, 공급업체 포털 가입, 본인 변경과 관리자 재설정에 모두 적용됩니다. `BOOTSTRAP_ADMIN_PASSWORD`도 72바이트를 넘으면 기동 시 명확한 오류로 거부되고, 최소 길이에 못 미치면 경고만 남긴 뒤 기동합니다.

### `security.login`

| 키 | 기본값 | 설명 |
|---|---|---|
| `maxFailures` | 5 | 한 계정을 잠그는 창 내 실패 횟수. `0`이면 계정 잠금을 끕니다. |
| `windowMinutes` | 15 | 실패를 집계하는 창 |
| `lockoutMinutes` | 15 | 마지막 실패 이후 잠금이 유지되는 시간 |
| `maxAddressFailures` | 25 | 한 발신 IP를 잠그는 창 내 실패 횟수. `0`이면 발신지 제한을 끕니다. |

범위를 벗어난 값은 기본값으로 되돌아가므로 잘못된 설정이 전체 로그인을 영구 차단할 수 없습니다. 로그인 실패는 `login_failed`, 잠금은 `login_locked` 감사 이벤트와 `vendra_login_failures_total`, `vendra_login_lockouts_total` 지표로 확인합니다.

## Authorization

- RBAC permissions support exact, domain wildcard(`supplier.*`) and read wildcard(`*.read`).
- Data Scope is `own`, `department`, `division`, `company`.
- Supplier Portal requests are forced to the user's supplier ID regardless of client input.
- 입찰 비교표는 제출 완료된 응답만 포함합니다. 공급업체가 제출 없이 저장한 초안의 금액과 품목은 구매자에게 보이지 않으며, 낙찰 선정도 제출 완료된 응답만 대상으로 합니다.
- 소싱 Q&A는 참여업체 공개 질문이라도 포털에서는 질문자 업체·담당자를 가립니다. 답변은 모두 읽을 수 있지만 함께 초대된 경쟁사를 알 수 없으며, 본인 질문과 구매팀 공지는 출처가 표시됩니다. 내부 검토자에게는 전체 출처가 보입니다.
- MCP 도구는 공급업체 포털 계정을 거부합니다. 모든 도구는 조직 범위로 결과를 좁히는 교차 공급업체 조회이므로 포털 격리를 담고 있지 않습니다. 기본 `supplier_user` 역할은 `portal.*`뿐이라 권한 게이트에서도 막히지만, 관리자가 조회 권한을 부여하더라도 사용자 유형만으로 차단됩니다.
- MCP 오류 응답은 의도된 거부 사유만 전달하고, 드라이버 오류나 캐스트 실패 같은 내부 결함은 로그로만 남깁니다.
- 데이터 범위는 조회뿐 아니라 쓰기에도 적용됩니다. 업무 객체를 만들거나 공급업체를 변경할 때 지정한 조직·공급업체가 범위 안인지 검증하므로, 접근할 수 없는 조직에 레코드를 만들어 넣을 수 없습니다.
- Amount fields are removed unless the Principal has the domain amount permission or wildcard.
- `access_grants` supports resource conditions, valid-from/until and delegated-by for temporary access/delegation.
- Workflow step role and object conditions enforce approval authority.
- 승인 단계는 상신 시점에 인스턴스로 스냅샷되므로, 관리자가 워크플로 정의를 바꿔도 이미 진행 중인 승인의 단계·순서·역할은 바뀌지 않습니다.
- `workflow.separation_of_duties`의 `blockSelfApproval`을 켜면 본인이 요청한 건을 승인하거나 반려할 수 없습니다. 보완 요청은 결재가 아니라 반환이므로 허용됩니다. 기본값은 꺼짐이며 감사 요건이 있는 조직은 켜는 것을 권장합니다.

## Encryption and audit

OIDC/AI secrets and bank accounts use AES-256-GCM with versioned ciphertext and authenticated context. `ENCRYPTION_KEY` is never stored in PostgreSQL. Audit events include actor, timestamp, action, object, before/after JSON, IP, session and request ID.

암호화 대상 값은 감사 기록에서 제외합니다. 공급업체 계좌는 변경 여부(`bankAccountChanged`)만 남고 번호 자체는 저장되지 않으며, 설정 Secret도 `secretChanged` 표시만 남습니다. 계좌 조회는 `supplier.bank_account.read` 권한으로 복호화 경로에서만 가능합니다.

Document upload limits size, strips paths from filenames, assigns an opaque storage name, records SHA-256 and validates that downloads stay under the configured storage root. 파일명은 업로더가 정하는 값이므로 `Content-Disposition`에 RFC 8187 ext-value로 완전히 percent-encoding해 내보내며, 파라미터를 추가하거나 벗어날 수 없습니다. `filename*`을 무시하는 클라이언트를 위해 ASCII `filename` 대체값도 함께 보냅니다.

사용자는 `개인화 및 보안 → 세션 및 보안`에서 자신의 활성 세션을 기기·IP·최근 활동 시각과 함께 확인하고 개별 또는 일괄 종료할 수 있습니다. 종료는 `revoke_session` 감사 이벤트로 남습니다.

## Reverse proxy

Use an allow-listed reverse proxy and TLS. Vendra emits CSP, frame denial, MIME sniffing protection, referrer and browser permissions policies. Set `secureCookie` in service administration after TLS is enabled.
