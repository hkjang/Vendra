# Vendra 엔터프라이즈 중장기 기술 로드맵 (Product Roadmap Plan)

- **문서 버전**: v1.0.0 ~ v3.0-VISION  
- **작성일자**: 2026년 8월 13일  
- **문서 분류**: 비즈니스 및 아키텍처 중장기 로드맵 (Strategic Product Roadmap)  

---

## 1. 비전 및 발전 마일스톤 개요

Vendra 플랫폼은 사내 오프라인 Supplier 360 및 조달 수명주기 자동화를 시작으로, 사내 AI 데이터 에이전트와 대화형으로 공급망 위험을 선제 예측하고 부품 단가 시뮬레이션을 자율 수행하는 차세대 Autonomous Supply Chain Intelligence Platform으로 진화합니다.

```
==================================================================================================
                                [Vendra 단계별 마일스톤 아키텍처]
==================================================================================================
 [Phase 1: v1.0.0] (완료) ➔ Supplier 360, Dynamic Scorecards, Portal Isolation, 11+ Read-Only MCP
 [Phase 2: v1.5.0] (진행) ➔ Global Multi-Tier Supply Chain Mapping & ERP (SAP/Oracle) Pipeline
 [Phase 3: v2.0.0] (2026 Q4) ➔ AI Autonomous Risk Early Warning Copilot (NL-to-SRM MCP 2.0)
 [Phase 4: v3.0.0] (2027)    ➔ Predictive Procurement Optimization & Zero-Trust Vendor Network
==================================================================================================
```

---

## 2. Phase별 세부 기술 명세 및 추진 전략

### 2.1 Phase 1: v1.0.0 오프라인 Supplier Intelligence SRM 구축 (완료)
- **Supplier 360 & 구매 수명주기**: 기본정보, 계약, PO, 검수, Invoice, 동적 스코어카드 산정.
- **사외 포털 격리**: Data Scope 서버 단 제한 및 계좌정보 AES-256-GCM 암호화.
- **ENCRYPTION_KEY & Keycloak OIDC**: Base64 32바이트 마스터 키 시크릿 보관, PKCE SSO 및 4대 환경변수 부트스트랩.
- **11+ Read-Only Streamable MCP**: AI 에이전트를 위한 파괴적 변경 차단 11개 조회 전용 MCP Tools.

### 2.2 Phase 2: v1.5.0 다단계 공급망 (Multi-Tier) 맵핑 & ERP 파이프라인 (2026 Q3)
- **1차~3차 벤더 다단계 맵핑**: 공급업체의 하도급 계열사 Network 위상 그래프 뷰 제공.
- **SAP/Oracle ERP 실시간 연동**: 사내 ERP 실제 세금계산서 및 대금 정산 파이프라인 동기화.

### 2.3 Phase 3: v2.0.0 AI 자율 조달 리스크 코파일럿 (2026 Q4)
- **NL-to-SRM Action (MCP 2.0)**: AI 에이전트에 "이번 달 납기 지연 고위험 벤더 3곳 추출하고 스코어카드 감점 리포트 작성해줘" 요청 시 권한 검증 후 자율 수행.

---

## 3. 리소스 및 위험 관리 (Risk Matrix)

| 위험 요소 | 영향도 | 발생 가능성 | 대응 및 완화 전략 |
| :--- | :--- | :--- | :--- |
| **PostgreSQL DB 장애** | High | Low | Multi-AZ HA 클러스터 및 Read-Replica 구축 |
| **ENCRYPTION_KEY 키 손실** | High | Low | Base64 마스터 키 안전한 이중화 백업 보관 |
| **사외 포털 권한 오설정** | High | Low | `supplier_id` 서버 단 필터링 및 침투 테스트 주기적 수행 |
