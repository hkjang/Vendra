import { useEffect, useState } from "react";
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  Building2,
  CalendarClock,
  CheckCircle2,
  CircleDollarSign,
  FileText,
  Sparkles,
  TrendingUp,
  Users,
} from "lucide-react";
import { Link } from "react-router-dom";
import { api, date, money } from "../api";
import {
  Badge,
  Empty,
  Loading,
  PageHeader,
  RiskBadge,
  ScoreRing,
} from "../components";
import { statusTone } from "../status";
import { BusinessObject, DashboardData } from "../types";

export default function Dashboard() {
  const [data, setData] = useState<DashboardData>();
  const [activity, setActivity] = useState<BusinessObject[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    Promise.all([
      api<DashboardData>("/api/v1/dashboard"),
      api<{ items: BusinessObject[] }>("/api/v1/contracts?limit=6"),
    ])
      .then(([d, o]) => {
        setData(d);
        setActivity(o.items);
      })
      .catch((e) => setError(e.message));
  }, []);
  if (!data && !error) return <Loading label="대시보드를 준비하는 중" />;
  if (error)
    return <Empty title="대시보드를 불러오지 못했습니다" description={error} />;
  const k = data!.kpis;
  return (
    <div className="page dashboard-page">
      <PageHeader
        eyebrow="Supplier command center"
        title="안녕하세요. 공급망 현황입니다."
        description="주요 공급업체 지표와 오늘 조치가 필요한 업무를 한눈에 확인하세요."
        actions={
          <>
            <span className="button secondary static-control">
              <CalendarClock />
              기간: 최근 12개월
            </span>
            <Link className="button" to="/suppliers">
              공급업체 보기
              <ArrowRight />
            </Link>
          </>
        }
      />
      <section className="kpi-grid">
        <KPI
          icon={<Building2 />}
          label="전체 공급업체"
          value={k.totalSuppliers.toLocaleString()}
          sub={`${k.activeSuppliers}개 거래 가능`}
          tone="blue"
        />
        <KPI
          icon={<CircleDollarSign />}
          label="연간 구매금액"
          value={compactMoney(k.annualSpend)}
          sub={`활성 계약 ${compactMoney(k.activeContractValue)}`}
          tone="mint"
        />
        <KPI
          icon={<TrendingUp />}
          label="평균 평가점수"
          value={`${k.averageScore.toFixed(1)}`}
          sub="100점 만점"
          tone="violet"
        />
        <KPI
          icon={<AlertTriangle />}
          label="High Risk"
          value={`${k.highRiskSuppliers}`}
          sub={`${k.openIssues}개 열린 이슈`}
          tone="orange"
        />
        <KPI
          icon={<CheckCircle2 />}
          label="납기 준수율"
          value={`${k.deliveryCompliance.toFixed(1)}%`}
          sub={`${k.overdueDeliveries}건 납기 지연`}
          tone="mint"
        />
        <KPI
          icon={<Activity />}
          label="불량률"
          value={`${k.defectRate.toFixed(1)}%`}
          sub="검수 · 품질 기준"
          tone="orange"
        />
      </section>
      <section className="dashboard-grid">
        <div className="card risk-overview">
          <div className="card-head">
            <div>
              <p className="eyebrow">Risk posture</p>
              <h2>공급망 리스크</h2>
            </div>
            <Link to="/risks">
              전체 보기
              <ArrowRight />
            </Link>
          </div>
          <div className="risk-chart">
            <div
              className="donut"
              style={
                {
                  "--critical": `${Math.min(65, k.highRiskSuppliers * 8)}deg`,
                } as React.CSSProperties
              }
            >
              <div>
                <strong>{k.highRiskSuppliers}</strong>
                <span>주의 필요</span>
              </div>
            </div>
            <div className="risk-legend">
              <div>
                <i className="critical" />
                <span>High · Critical</span>
                <b>{k.highRiskSuppliers}</b>
              </div>
              <div>
                <i className="medium" />
                <span>계약 만료 임박</span>
                <b>{k.expiringContracts}</b>
              </div>
              <div>
                <i className="low" />
                <span>정상 거래 업체</span>
                <b>{k.activeSuppliers}</b>
              </div>
            </div>
          </div>
          <div className="attention">
            <AlertTriangle />
            <div>
              <b>오늘의 리스크 브리핑</b>
              <p>고위험 공급업체와 180일 내 만료 계약을 우선 검토하세요.</p>
            </div>
            <Link to="/ai">
              <Sparkles />
              AI 분석
            </Link>
          </div>
        </div>
        <div className="card supplier-ranking">
          <div className="card-head">
            <div>
              <p className="eyebrow">Spend concentration</p>
              <h2>구매금액 Top 공급업체</h2>
            </div>
            <Link to="/suppliers">전체 보기</Link>
          </div>
          {data!.topSuppliers.length ? (
            <div className="rank-list">
              {data!.topSuppliers.map((s, i) => (
                <Link to={`/suppliers/${s.id}`} key={s.id}>
                  <span className="rank">{String(i + 1).padStart(2, "0")}</span>
                  <span className="company-avatar">{s.name.slice(0, 2)}</span>
                  <span className="rank-name">
                    <b>{s.name}</b>
                    <small>{money(s.annualSpend)}</small>
                  </span>
                  <ScoreRing score={s.score} size="small" />
                  <RiskBadge level={s.riskLevel} />
                </Link>
              ))}
            </div>
          ) : (
            <Empty
              title="공급업체 데이터가 없습니다"
              description="공급업체를 등록하면 구매 집중도를 표시합니다."
            />
          )}
        </div>
        <div className="card activity-card">
          <div className="card-head">
            <div>
              <p className="eyebrow">Contract watch</p>
              <h2>최근 계약</h2>
            </div>
            <Link to="/contracts">
              계약 전체
              <ArrowRight />
            </Link>
          </div>
          {activity.length ? (
            <div className="timeline">
              {activity.map((o) => (
                <div key={o.id}>
                  <span className={`timeline-icon ${statusTone(o.status)}`}>
                    <FileText />
                  </span>
                  <div>
                    <b>{o.title}</b>
                    <p>
                      {o.supplierName || "공급업체 미지정"} · {o.number}
                    </p>
                  </div>
                  <span>
                    <Badge tone={statusTone(o.status)}>{o.status}</Badge>
                    <small>{date(o.endDate || o.updatedAt)}</small>
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <Empty
              icon={<FileText />}
              title="계약이 없습니다"
              description="새 계약을 등록하면 이곳에서 만료와 상태를 추적합니다."
            />
          )}
        </div>
        <div className="card work-queue">
          <div className="card-head">
            <div>
              <p className="eyebrow">My work</p>
              <h2>업무 대기열</h2>
            </div>
            <Link to="/approvals">
              승인함
              <ArrowRight />
            </Link>
          </div>
          <div className="queue-grid">
            <Link to="/approvals">
              <span className="queue-icon amber">
                <ClipboardIcon />
              </span>
              <div>
                <b>승인 대기</b>
                <strong>{k.pendingApprovals}</strong>
              </div>
            </Link>
            <Link to="/suppliers?status=screening">
              <span className="queue-icon blue">
                <Users />
              </span>
              <div>
                <b>신규 업체 심사</b>
                <strong>{k.pendingScreenings}</strong>
              </div>
            </Link>
            <Link to="/rfq">
              <span className="queue-icon violet">
                <CalendarClock />
              </span>
              <div>
                <b>RFQ · RFP 진행</b>
                <strong>{k.activeRFQ + k.activeRFP}</strong>
              </div>
            </Link>
            <Link to="/issues">
              <span className="queue-icon red">
                <Activity />
              </span>
              <div>
                <b>열린 이슈</b>
                <strong>{k.openIssues}</strong>
              </div>
            </Link>
          </div>
          <div className="health-strip">
            <CheckCircle2 />
            <span>
              <b>서비스 정상</b> · 모든 핵심 시스템이 작동 중입니다.
            </span>
          </div>
        </div>
      </section>
    </div>
  );
}

function KPI({
  icon,
  label,
  value,
  sub,
  tone,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  sub: string;
  tone: string;
}) {
  return (
    <div className={`kpi-card ${tone}`}>
      <div className="kpi-icon">{icon}</div>
      <div>
        <span>{label}</span>
        <strong>{value}</strong>
        <small>{sub}</small>
      </div>
    </div>
  );
}
function compactMoney(v: number) {
  if (v >= 1e12) return `${(v / 1e12).toFixed(1)}조`;
  if (v >= 1e8) return `${(v / 1e8).toFixed(1)}억`;
  if (v >= 1e4) return `${(v / 1e4).toFixed(0)}만`;
  return money(v);
}
function ClipboardIcon() {
  return (
    <svg viewBox="0 0 24 24">
      <path d="M9 5h6m-7 3h8m-9 4h10m-10 4h6M7 3h10a2 2 0 0 1 2 2v15H5V5a2 2 0 0 1 2-2Z" />
    </svg>
  );
}
