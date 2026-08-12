import { FormEvent, useCallback, useEffect, useState } from "react";
import {
  Activity,
  AlertCircle,
  ArrowLeft,
  ArrowRight,
  Building2,
  ChevronRight,
  CircleDollarSign,
  ClipboardCheck,
  Download,
  FileText,
  Filter,
  Globe2,
  Mail,
  MapPin,
  MoreHorizontal,
  Network,
  Phone,
  Plus,
  Search,
  ShieldAlert,
  SlidersHorizontal,
  Upload,
  Users,
} from "lucide-react";
import {
  Link,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";
import { api, date, money, patch, post } from "../api";
import {
  Badge,
  Empty,
  Field,
  Loading,
  Modal,
  PageHeader,
  RiskBadge,
  ScoreRing,
  statusTone,
} from "../components";
import { BusinessObject, Supplier } from "../types";

export default function Suppliers() {
  const [params, setParams] = useSearchParams();
  const [items, setItems] = useState<Supplier[]>([]);
  const [loading, setLoading] = useState(true);
  const [modal, setModal] = useState(false);
  const [view, setView] = useState<"table" | "cards">("table");
  const q = params.get("q") || "";
  const status = params.get("status") || "";
  const risk = params.get("risk") || "";
  const load = useCallback(() => {
    setLoading(true);
    api<{ items: Supplier[] }>(
      `/api/v1/suppliers?q=${encodeURIComponent(q)}&status=${status}&risk=${risk}`,
    )
      .then((x) => setItems(x.items))
      .finally(() => setLoading(false));
  }, [q, status, risk]);
  useEffect(load, [load]);
  function set(key: string, value: string) {
    const n = new URLSearchParams(params);
    value ? n.set(key, value) : n.delete(key);
    setParams(n);
  }
  return (
    <div className="page">
      <PageHeader
        eyebrow="Supplier master"
        title="공급업체"
        description="후보 발굴부터 거래 중단까지 공급업체의 전체 생명주기를 관리합니다."
        actions={
          <>
            <button className="button secondary">
              <Upload />
              가져오기
            </button>
            <button className="button" onClick={() => setModal(true)}>
              <Plus />
              공급업체 등록
            </button>
          </>
        }
      />
      <div className="toolbar">
        <div className="search-box">
          <Search />
          <input
            value={q}
            onChange={(e) => set("q", e.target.value)}
            placeholder="업체명, 사업자번호, 공급업체 번호 검색"
          />
        </div>
        <select value={status} onChange={(e) => set("status", e.target.value)}>
          <option value="">모든 상태</option>
          <option value="candidate">후보</option>
          <option value="registration">등록</option>
          <option value="screening">심사</option>
          <option value="approved">승인</option>
          <option value="active">거래 가능</option>
          <option value="improvement">개선 대상</option>
          <option value="suspended">거래 중단</option>
        </select>
        <select value={risk} onChange={(e) => set("risk", e.target.value)}>
          <option value="">모든 리스크</option>
          <option>LOW</option>
          <option>MEDIUM</option>
          <option>HIGH</option>
          <option>CRITICAL</option>
        </select>
        <button className="button ghost">
          <SlidersHorizontal />
          필터
        </button>
        <div className="view-toggle">
          <button
            className={view === "table" ? "active" : ""}
            onClick={() => setView("table")}
          >
            목록
          </button>
          <button
            className={view === "cards" ? "active" : ""}
            onClick={() => setView("cards")}
          >
            카드
          </button>
        </div>
      </div>
      {loading ? (
        <Loading />
      ) : items.length === 0 ? (
        <Empty
          icon={<Building2 />}
          title="조건에 맞는 공급업체가 없습니다"
          description="검색 조건을 변경하거나 첫 공급업체를 등록하세요."
          action={
            <button className="button" onClick={() => setModal(true)}>
              <Plus />
              공급업체 등록
            </button>
          }
        />
      ) : view === "table" ? (
        <SupplierTable items={items} />
      ) : (
        <SupplierCards items={items} />
      )}{" "}
      {modal && (
        <NewSupplier
          onClose={() => setModal(false)}
          onSaved={() => {
            setModal(false);
            load();
          }}
        />
      )}
    </div>
  );
}

function SupplierTable({ items }: { items: Supplier[] }) {
  return (
    <div className="data-card">
      <table>
        <thead>
          <tr>
            <th>공급업체</th>
            <th>상태</th>
            <th>평가</th>
            <th>Risk</th>
            <th>연간 구매금액</th>
            <th>업종 · 품목</th>
            <th>최근 변경</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {items.map((s) => (
            <tr key={s.id}>
              <td>
                <Link className="supplier-cell" to={`/suppliers/${s.id}`}>
                  <span className="company-avatar">{s.name.slice(0, 2)}</span>
                  <span>
                    <b>{s.name}</b>
                    <small>
                      {s.supplierNumber} · {s.businessNumber}
                    </small>
                  </span>
                </Link>
              </td>
              <td>
                <Badge tone={statusTone(s.status)}>
                  {statusLabel(s.status)}
                </Badge>
              </td>
              <td>
                <span className="grade">
                  <b>{s.grade || "—"}</b>
                  <small>
                    {s.score == null ? "미평가" : `${s.score.toFixed(1)}점`}
                  </small>
                </span>
              </td>
              <td>
                <RiskBadge level={s.riskLevel} />
              </td>
              <td className="number">{money(s.annualSpend)}</td>
              <td>
                <span className="stack">
                  <b>{s.industry || "미지정"}</b>
                  <small>
                    {s.categories?.slice(0, 2).join(", ") || "품목 미지정"}
                  </small>
                </span>
              </td>
              <td>{date(s.updatedAt)}</td>
              <td>
                <Link className="icon-button" to={`/suppliers/${s.id}`}>
                  <ChevronRight />
                </Link>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="table-footer">
        <span>총 {items.length.toLocaleString()}개 공급업체</span>
        <div>페이지당 100개</div>
      </div>
    </div>
  );
}
function SupplierCards({ items }: { items: Supplier[] }) {
  return (
    <div className="supplier-cards">
      {items.map((s) => (
        <Link to={`/suppliers/${s.id}`} className="supplier-card" key={s.id}>
          <header>
            <span className="company-avatar large">{s.name.slice(0, 2)}</span>
            <div>
              <b>{s.name}</b>
              <small>{s.supplierNumber}</small>
            </div>
            <RiskBadge level={s.riskLevel} />
          </header>
          <div className="supplier-card-metrics">
            <ScoreRing score={s.score} size="small" />
            <div>
              <span>평가등급</span>
              <b>{s.grade || "—"}</b>
            </div>
            <div>
              <span>연간 구매</span>
              <b>{money(s.annualSpend)}</b>
            </div>
          </div>
          <footer>
            <Badge tone={statusTone(s.status)}>{statusLabel(s.status)}</Badge>
            <span>{s.industry || "업종 미지정"}</span>
            <ArrowRight />
          </footer>
        </Link>
      ))}
    </div>
  );
}

function NewSupplier({
  onClose,
  onSaved,
}: {
  onClose: () => void;
  onSaved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError("");
    const d = new FormData(e.currentTarget);
    try {
      await post("/api/v1/suppliers", {
        name: d.get("name"),
        legalName: d.get("legalName"),
        businessNumber: d.get("businessNumber"),
        representative: d.get("representative"),
        industry: d.get("industry"),
        supplierType: d.get("supplierType"),
        email: d.get("email"),
        phone: d.get("phone"),
        status: "candidate",
        riskLevel: "LOW",
        categories: String(d.get("categories") || "")
          .split(",")
          .map((x) => x.trim())
          .filter(Boolean),
      });
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "저장하지 못했습니다");
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      title="새 공급업체 등록"
      description="기본 정보를 등록한 뒤 Supplier 360에서 심사, 계약, 리스크 정보를 추가할 수 있습니다."
      onClose={onClose}
      wide
    >
      <form onSubmit={submit}>
        <div className="form-grid">
          <Field label="업체명" required>
            <input
              name="name"
              required
              autoFocus
              placeholder="주식회사 벤드라"
            />
          </Field>
          <Field label="법인명">
            <input name="legalName" placeholder="등기상 법인명" />
          </Field>
          <Field
            label="사업자번호"
            required
            hint="중복 등록 여부를 자동 확인합니다."
          >
            <input name="businessNumber" required placeholder="000-00-00000" />
          </Field>
          <Field label="대표자">
            <input name="representative" />
          </Field>
          <Field label="공급업체 유형">
            <select name="supplierType">
              <option value="">선택</option>
              <option>제조</option>
              <option>유통</option>
              <option>서비스</option>
              <option>용역</option>
              <option>IT</option>
            </select>
          </Field>
          <Field label="업종">
            <input name="industry" placeholder="소프트웨어 개발 및 공급" />
          </Field>
          <Field label="공급 품목" hint="쉼표로 여러 품목을 구분하세요.">
            <input name="categories" placeholder="클라우드, 보안, 컨설팅" />
          </Field>
          <Field label="대표 이메일">
            <input name="email" type="email" />
          </Field>
          <Field label="대표 전화">
            <input name="phone" />
          </Field>
        </div>
        {error && (
          <div className="form-error">
            <AlertCircle />
            {error}
          </div>
        )}
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button" disabled={busy}>
            {busy ? "등록 중…" : "후보 공급업체 등록"}
          </button>
        </div>
      </form>
    </Modal>
  );
}

function SupplierEdit({
  supplier,
  onClose,
  onSaved,
}: {
  supplier: Supplier;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [error, setError] = useState("");
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    try {
      await patch(`/api/v1/suppliers/${supplier.id}`, {
        name: d.get("name"),
        legalName: d.get("legalName"),
        representative: d.get("representative"),
        status: d.get("status"),
        supplierType: d.get("supplierType"),
        industry: d.get("industry"),
        email: d.get("email"),
        phone: d.get("phone"),
        website: d.get("website"),
        erpVendorId: d.get("erpVendorId"),
        bankAccount: d.get("bankAccount"),
        categories: String(d.get("categories") || "")
          .split(",")
          .map((x) => x.trim())
          .filter(Boolean),
        financials: {
          ...supplier.financials,
          revenue: Number(d.get("revenue")) || 0,
          operatingProfit: Number(d.get("operatingProfit")) || 0,
          capital: Number(d.get("capital")) || 0,
        },
        taxInfo: {
          ...supplier.taxInfo,
          invoiceEmail: d.get("invoiceEmail"),
          taxRegistration: d.get("taxRegistration"),
        },
      });
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "저장하지 못했습니다");
    }
  }
  return (
    <Modal
      title="공급업체 Master 편집"
      description="계좌 변경은 관리자 정책에 따라 별도 승인 Workflow를 시작합니다."
      onClose={onClose}
      wide
    >
      <form onSubmit={submit}>
        <div className="form-grid">
          <Field label="업체명" required>
            <input
              name="name"
              defaultValue={supplier.name}
              required
              autoFocus
            />
          </Field>
          <Field label="법인명">
            <input name="legalName" defaultValue={supplier.legalName} />
          </Field>
          <Field label="대표자">
            <input
              name="representative"
              defaultValue={supplier.representative}
            />
          </Field>
          <Field label="거래 상태">
            <select name="status" defaultValue={supplier.status}>
              {[
                "candidate",
                "registered",
                "screening",
                "approved",
                "active",
                "preferred",
                "improvement",
                "suspended",
                "terminated",
              ].map((value) => (
                <option key={value}>{value}</option>
              ))}
            </select>
          </Field>
          <Field label="공급업체 유형">
            <input name="supplierType" defaultValue={supplier.supplierType} />
          </Field>
          <Field label="업종">
            <input name="industry" defaultValue={supplier.industry} />
          </Field>
          <Field label="공급 품목">
            <input
              name="categories"
              defaultValue={supplier.categories.join(", ")}
            />
          </Field>
          <Field label="ERP Vendor ID">
            <input name="erpVendorId" defaultValue={supplier.erpVendorId} />
          </Field>
          <Field label="대표 이메일">
            <input name="email" type="email" defaultValue={supplier.email} />
          </Field>
          <Field label="대표 전화">
            <input name="phone" defaultValue={supplier.phone} />
          </Field>
          <Field label="웹사이트">
            <input name="website" defaultValue={supplier.website} />
          </Field>
          <Field
            label="새 지급 계좌"
            hint={
              supplier.bankAccount
                ? `현재 계좌 ••••${supplier.bankAccount.slice(-4)}`
                : "권한이 있으면 계좌 상태를 확인할 수 있습니다."
            }
          >
            <input
              name="bankAccount"
              type="password"
              autoComplete="new-password"
              placeholder="변경할 때만 입력"
            />
          </Field>
          <Field label="매출">
            <input
              name="revenue"
              type="number"
              min="0"
              defaultValue={Number(supplier.financials?.revenue || 0)}
            />
          </Field>
          <Field label="영업이익">
            <input
              name="operatingProfit"
              type="number"
              defaultValue={Number(supplier.financials?.operatingProfit || 0)}
            />
          </Field>
          <Field label="자본금">
            <input
              name="capital"
              type="number"
              min="0"
              defaultValue={Number(supplier.financials?.capital || 0)}
            />
          </Field>
          <Field label="세금계산서 이메일">
            <input
              name="invoiceEmail"
              type="email"
              defaultValue={String(supplier.taxInfo?.invoiceEmail || "")}
            />
          </Field>
          <Field label="세무 등록정보">
            <input
              name="taxRegistration"
              defaultValue={String(supplier.taxInfo?.taxRegistration || "")}
            />
          </Field>
        </div>
        {error && (
          <div className="form-error">
            <AlertCircle />
            {error}
          </div>
        )}
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button">Master 저장</button>
        </div>
      </form>
    </Modal>
  );
}

function SupplierInvitation({
  supplier,
  onClose,
}: {
  supplier: Supplier;
  onClose: () => void;
}) {
  const [url, setURL] = useState("");
  const [error, setError] = useState("");
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    try {
      const result = await post<{ invitationUrl: string }>(
        "/api/v1/invitations",
        {
          email: d.get("email"),
          supplierId: supplier.id,
          expiresInDays: Number(d.get("expiresInDays")),
        },
      );
      setURL(`${window.location.origin}${result.invitationUrl}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "초대를 만들지 못했습니다");
    }
  }
  return (
    <Modal
      title="Supplier Portal 초대"
      description={`${supplier.name} 담당자에게 일회성 Self Registration 링크를 발급합니다.`}
      onClose={onClose}
    >
      <form onSubmit={submit}>
        <Field label="담당자 이메일" required>
          <input
            name="email"
            type="email"
            defaultValue={supplier.email}
            required
            autoFocus
          />
        </Field>
        <Field label="유효기간">
          <select name="expiresInDays" defaultValue="7">
            <option value="1">1일</option>
            <option value="3">3일</option>
            <option value="7">7일</option>
            <option value="14">14일</option>
          </select>
        </Field>
        {url && (
          <div className="secret-reveal">
            <code>{url}</code>
            <button
              type="button"
              className="button"
              onClick={() => navigator.clipboard.writeText(url)}
            >
              링크 복사
            </button>
          </div>
        )}
        {error && (
          <div className="form-error">
            <AlertCircle />
            {error}
          </div>
        )}
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            닫기
          </button>
          <button className="button">
            <Mail />
            초대 링크 발급
          </button>
        </div>
      </form>
    </Modal>
  );
}

type DetailResponse = {
  supplier: Supplier;
  metrics: {
    activeContracts: number;
    openIssues: number;
    deliveryPerformance: number;
    qualityPerformance: number;
  };
};
const tabs = [
  "Overview",
  "Contacts",
  "Screening",
  "Contracts",
  "Purchase Orders",
  "Deliveries",
  "Quality",
  "Evaluations",
  "Risks",
  "Issues",
  "Documents",
  "Spend",
  "Activity",
  "Audit Log",
];
export function SupplierDetail() {
  const { id } = useParams();
  const nav = useNavigate();
  const [params] = useSearchParams();
  const [data, setData] = useState<DetailResponse>();
  const [objects, setObjects] = useState<BusinessObject[]>([]);
  const [tab, setTab] = useState(params.get("tab") || "Overview");
  const [loading, setLoading] = useState(true);
  const [upload, setUpload] = useState(false);
  const [edit, setEdit] = useState(false);
  const [invite, setInvite] = useState(false);
  const load = useCallback(
    () =>
      Promise.all([
        api<DetailResponse>(`/api/v1/suppliers/${id}`),
        api<{ items: BusinessObject[] }>(`/api/v1/suppliers/${id}/objects`),
      ]).then(([d, o]) => {
        setData(d);
        setObjects(o.items);
      }),
    [id],
  );
  useEffect(() => {
    load().finally(() => setLoading(false));
  }, [load]);
  if (loading) return <Loading />;
  if (!data)
    return (
      <Empty
        title="공급업체를 찾을 수 없습니다"
        description="삭제되었거나 접근 권한이 없는 공급업체입니다."
      />
    );
  const s = data.supplier;
  return (
    <div className="page supplier-detail">
      <button className="back-link" onClick={() => nav("/suppliers")}>
        <ArrowLeft />
        공급업체 목록
      </button>
      <section className="supplier-hero">
        <div className="company-avatar hero-avatar">{s.name.slice(0, 2)}</div>
        <div className="supplier-title">
          <div>
            <span className="eyebrow">{s.supplierNumber}</span>
            <h1>{s.name}</h1>
            <p>{s.legalName || s.industry || "공급업체 상세정보"}</p>
          </div>
          <div className="supplier-badges">
            <Badge tone={statusTone(s.status)}>{statusLabel(s.status)}</Badge>
            <RiskBadge level={s.riskLevel} />
          </div>
        </div>
        <div className="hero-actions">
          <button className="button secondary" onClick={() => setInvite(true)}>
            <Mail />
            포털 초대
          </button>
          <button className="button secondary" onClick={() => setUpload(true)}>
            <Upload />
            문서 등록
          </button>
          <button className="button" onClick={() => setEdit(true)}>
            <MoreHorizontal />
            관리
          </button>
        </div>
      </section>
      <section className="supplier-vitals">
        <div>
          <span>Supplier grade</span>
          <strong className="grade-large">{s.grade || "—"}</strong>
          <small>
            {s.score == null ? "평가 전" : `${s.score.toFixed(1)}점`}
          </small>
        </div>
        <div>
          <span>Risk</span>
          <RiskBadge level={s.riskLevel} />
          <small>지속 모니터링</small>
        </div>
        <div>
          <span>Annual spend</span>
          <strong>{money(s.annualSpend)}</strong>
          <small>최근 12개월</small>
        </div>
        <div>
          <span>Active contracts</span>
          <strong>{data.metrics.activeContracts}</strong>
          <small>활성 계약</small>
        </div>
        <div>
          <span>Performance</span>
          <strong>{data.metrics.deliveryPerformance.toFixed(1)}%</strong>
          <small>납기 준수율</small>
        </div>
        <div>
          <span>Open issues</span>
          <strong className={data.metrics.openIssues ? "danger-text" : ""}>
            {data.metrics.openIssues}
          </strong>
          <small>조치 필요</small>
        </div>
      </section>
      <div className="tabs">
        {tabs.map((t) => (
          <button
            className={tab === t ? "active" : ""}
            onClick={() => setTab(t)}
            key={t}
          >
            {t}
          </button>
        ))}
      </div>
      <SupplierTab
        tab={tab}
        data={data}
        objects={objects}
        onEdit={() => setEdit(true)}
      />
      {upload && (
        <DocumentUpload
          supplierId={s.id}
          onClose={() => setUpload(false)}
          onSaved={() => {
            setUpload(false);
            setTab("Documents");
          }}
        />
      )}
      {edit && (
        <SupplierEdit
          supplier={s}
          onClose={() => setEdit(false)}
          onSaved={() => {
            setEdit(false);
            load();
          }}
        />
      )}
      {invite && (
        <SupplierInvitation supplier={s} onClose={() => setInvite(false)} />
      )}
    </div>
  );
}

export function DocumentUpload({
  supplierId,
  onClose,
  onSaved,
  portal = false,
}: {
  supplierId?: string;
  onClose: () => void;
  onSaved: () => void;
  portal?: boolean;
}) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError("");
    const body = new FormData(e.currentTarget);
    if (supplierId) body.set("supplierId", supplierId);
    try {
      const response = await fetch(
        portal ? "/api/v1/portal/documents/upload" : "/api/v1/documents/upload",
        { method: "POST", body, credentials: "same-origin" },
      );
      const result = await response.json().catch(() => ({}));
      if (!response.ok)
        throw new Error(result?.error?.message || "업로드하지 못했습니다");
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "업로드하지 못했습니다");
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      title="문서 등록"
      description="동일한 문서유형과 파일명은 새 버전으로 저장되며 SHA-256 Checksum을 기록합니다."
      onClose={onClose}
    >
      <form onSubmit={submit}>
        <Field label="문서 유형">
          <select name="documentType">
            <option value="business_registration">사업자등록증</option>
            <option value="financial_statement">재무제표</option>
            <option value="nda">NDA</option>
            <option value="contract">계약서</option>
            <option value="quality_certificate">품질 인증서</option>
            <option value="insurance">보험증권</option>
            <option value="iso">ISO 인증</option>
            <option value="proposal">제안서</option>
            <option value="quotation">견적서</option>
            <option value="other">기타</option>
          </select>
        </Field>
        <Field label="만료일">
          <input name="expiresAt" type="date" />
        </Field>
        <Field label="파일" required hint="최대 25MB">
          <input name="file" type="file" required />
        </Field>
        {error && (
          <div className="form-error">
            <AlertCircle />
            {error}
          </div>
        )}
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button" disabled={busy}>
            {busy ? "업로드 중…" : "업로드"}
          </button>
        </div>
      </form>
    </Modal>
  );
}

function SupplierTab({
  tab,
  data,
  objects,
  onEdit,
}: {
  tab: string;
  data: DetailResponse;
  objects: BusinessObject[];
  onEdit: () => void;
}) {
  const s = data.supplier;
  if (tab === "Overview")
    return (
      <div className="detail-grid">
        <div className="card info-card">
          <div className="card-head">
            <h2>업체 기본정보</h2>
            <button className="button ghost" onClick={onEdit}>
              수정
            </button>
          </div>
          <dl>
            <dt>사업자번호</dt>
            <dd>{s.businessNumber}</dd>
            <dt>대표자</dt>
            <dd>{s.representative || "—"}</dd>
            <dt>업종</dt>
            <dd>{s.industry || "—"}</dd>
            <dt>공급 품목</dt>
            <dd>{s.categories?.join(", ") || "—"}</dd>
            <dt>ERP Vendor ID</dt>
            <dd>{s.erpVendorId || "—"}</dd>
            <dt>지급 계좌</dt>
            <dd>
              {s.bankAccount
                ? `•••• ${s.bankAccount.slice(-4)}`
                : "권한 제한 또는 미등록"}
            </dd>
            <dt>거래 상태</dt>
            <dd>
              <Badge tone={statusTone(s.status)}>{statusLabel(s.status)}</Badge>
            </dd>
          </dl>
        </div>
        <div className="card performance-card">
          <div className="card-head">
            <h2>성과 스냅샷</h2>
            <span>최근 평가</span>
          </div>
          <div className="performance-main">
            <ScoreRing score={s.score} />
            <div>
              <strong>{s.grade || "미평가"} 등급</strong>
              <p>동적 Scorecard 기준</p>
            </div>
          </div>
          <div className="metric-bars">
            <Metric label="납기" value={data.metrics.deliveryPerformance} />
            <Metric label="품질" value={data.metrics.qualityPerformance} />
            <Metric label="종합" value={s.score || 0} />
          </div>
        </div>
        <div className="card contact-card">
          <div className="card-head">
            <h2>연락처</h2>
          </div>
          <p>
            <Mail />
            {s.email || "등록된 이메일 없음"}
          </p>
          <p>
            <Phone />
            {s.phone || "등록된 전화번호 없음"}
          </p>
          <p>
            <Globe2 />
            {s.website || "등록된 웹사이트 없음"}
          </p>
          <p>
            <MapPin />
            주소 정보를 확인하세요
          </p>
        </div>
        <div className="card recent-card">
          <div className="card-head">
            <h2>최근 업무</h2>
          </div>
          {objects.slice(0, 5).map((o) => (
            <div className="recent-row" key={o.id}>
              <span className="object-icon">
                <FileText />
              </span>
              <div>
                <b>{o.title}</b>
                <small>
                  {o.number} · {date(o.updatedAt)}
                </small>
              </div>
              <Badge tone={statusTone(o.status)}>{o.status}</Badge>
            </div>
          ))}
          {!objects.length && (
            <Empty
              title="연결된 업무가 없습니다"
              description="계약, 발주, 품질, 이슈 데이터가 여기에 표시됩니다."
            />
          )}
        </div>
      </div>
    );
  const map: Record<string, string> = {
    Contracts: "contract",
    "Purchase Orders": "purchase_order",
    Deliveries: "delivery",
    Quality: "quality",
    Issues: "issue",
  };
  if (map[tab]) {
    const filtered = objects.filter((o) => o.objectType === map[tab]);
    return <RelatedList items={filtered} label={tab} />;
  }
  if (tab === "Spend")
    return (
      <div className="card spend-summary">
        <CircleDollarSign />
        <h2>연간 구매금액</h2>
        <strong>{money(s.annualSpend)}</strong>
        <p>
          전체 공급업체 의존도와 조직·품목별 구매 분석은 Spend 분석 화면에서
          확인할 수 있습니다.
        </p>
        <Link className="button" to="/spend">
          Spend 분석 열기
          <ArrowRight />
        </Link>
      </div>
    );
  if (tab === "Contacts")
    return (
      <AsyncSub
        endpoint={`/api/v1/suppliers/${s.id}/contacts`}
        empty="등록된 담당자가 없습니다."
      />
    );
  if (tab === "Screening") return <ScreeningPanel supplierId={s.id} />;
  if (tab === "Risks") return <RiskPanel supplierId={s.id} />;
  if (tab === "Evaluations") return <EvaluationPanel supplierId={s.id} />;
  if (tab === "Documents") return <DocumentsPanel supplierId={s.id} />;
  if (tab === "Activity" || tab === "Audit Log")
    return (
      <AsyncSub
        endpoint={`/api/v1/suppliers/${s.id}/activity`}
        empty="활동 이력이 없습니다."
      />
    );
  return null;
}

type ScorecardTemplate = {
  id: string;
  name: string;
  evaluationType: string;
  criteria: { code: string; name: string; weight: number }[];
};

function EvaluationPanel({ supplierId }: { supplierId: string }) {
  const [items, setItems] = useState<Record<string, unknown>[]>();
  const [templates, setTemplates] = useState<ScorecardTemplate[]>();
  const [create, setCreate] = useState(false);
  const load = useCallback(
    () =>
      Promise.all([
        api<{ items: Record<string, unknown>[] }>(
          `/api/v1/suppliers/${supplierId}/evaluations`,
        ),
        api<{ items: ScorecardTemplate[] }>("/api/v1/scorecards"),
      ]).then(([e, t]) => {
        setItems(e.items);
        setTemplates(t.items);
      }),
    [supplierId],
  );
  useEffect(() => {
    void load();
  }, [load]);
  if (!items || !templates) return <Loading />;
  return (
    <section className="card domain-panel">
      <div className="card-head">
        <div>
          <h2>360° 공급업체 평가</h2>
          <p>신규·정기·프로젝트·계약·품질·납기·보안·긴급 평가를 누적합니다.</p>
        </div>
        <button className="button" onClick={() => setCreate(true)}>
          <Plus /> 평가 등록
        </button>
      </div>
      <div className="domain-list">
        {items.map((item, index) => (
          <article key={String(item.id || index)}>
            <span className="object-icon">
              <ClipboardCheck />
            </span>
            <div>
              <b>{String(item.templateName || item.evaluationType)}</b>
              <small>
                {String(item.evaluationType)} · {date(String(item.createdAt))}
              </small>
            </div>
            <strong>
              {item.totalScore == null
                ? "—"
                : Number(item.totalScore).toFixed(1)}
            </strong>
            <Badge tone={statusTone(String(item.grade || item.status))}>
              {String(item.grade || item.status)}
            </Badge>
          </article>
        ))}
      </div>
      {!items.length && (
        <Empty
          title="등록된 평가가 없습니다"
          description="동적 Scorecard로 첫 평가를 시작하세요."
        />
      )}
      {create && (
        <NewEvaluation
          supplierId={supplierId}
          templates={templates}
          onClose={() => setCreate(false)}
          onSaved={() => {
            setCreate(false);
            load();
          }}
        />
      )}
    </section>
  );
}

function NewEvaluation({
  supplierId,
  templates,
  onClose,
  onSaved,
}: {
  supplierId: string;
  templates: ScorecardTemplate[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [templateId, setTemplateId] = useState(templates[0]?.id || "");
  const template = templates.find((x) => x.id === templateId);
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    const scores: Record<string, number> = {};
    template?.criteria.forEach((c) => {
      scores[c.code] = Number(d.get(c.code));
    });
    await post(`/api/v1/suppliers/${supplierId}/evaluations`, {
      templateId,
      evaluationType: d.get("evaluationType"),
      periodStart: d.get("periodStart"),
      periodEnd: d.get("periodEnd"),
      scores,
      comments: d.get("comments"),
      status: "completed",
    });
    onSaved();
  }
  return (
    <Modal
      title="공급업체 평가"
      description="가중점수와 등급은 선택한 Scorecard 기준으로 자동 산정됩니다."
      onClose={onClose}
      wide
    >
      <form onSubmit={submit}>
        <div className="form-grid">
          <Field label="Scorecard">
            <select
              value={templateId}
              onChange={(e) => setTemplateId(e.target.value)}
            >
              {templates.map((t) => (
                <option value={t.id} key={t.id}>
                  {t.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="평가 유형">
            <select name="evaluationType">
              <option value="new_supplier">신규업체 평가</option>
              <option value="periodic">정기 평가</option>
              <option value="project">프로젝트 평가</option>
              <option value="contract">계약 평가</option>
              <option value="quality">품질 평가</option>
              <option value="delivery">납기 평가</option>
              <option value="security">보안 평가</option>
              <option value="emergency">긴급 평가</option>
            </select>
          </Field>
          <Field label="평가 시작">
            <input name="periodStart" type="date" />
          </Field>
          <Field label="평가 종료">
            <input name="periodEnd" type="date" />
          </Field>
        </div>
        <div className="evaluation-inputs">
          {template?.criteria.map((c) => (
            <label key={c.code}>
              <span>
                <b>{c.name}</b>
                <small>가중치 {c.weight}%</small>
              </span>
              <input name={c.code} type="number" min="0" max="100" required />
              <em>/100</em>
            </label>
          ))}
        </div>
        <Field label="평가 의견">
          <textarea name="comments" rows={3} />
        </Field>
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button">평가 완료</button>
        </div>
      </form>
    </Modal>
  );
}

function RiskPanel({ supplierId }: { supplierId: string }) {
  const [items, setItems] = useState<Record<string, unknown>[]>();
  const [create, setCreate] = useState(false);
  const load = useCallback(
    () =>
      api<{ items: Record<string, unknown>[] }>(
        `/api/v1/suppliers/${supplierId}/risks`,
      ).then((x) => setItems(x.items)),
    [supplierId],
  );
  useEffect(() => {
    void load();
  }, [load]);
  if (!items) return <Loading />;
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    await post(`/api/v1/suppliers/${supplierId}/risks`, {
      riskType: d.get("riskType"),
      severity: d.get("severity"),
      probability: Number(d.get("probability")),
      impact: Number(d.get("impact")),
      description: d.get("description"),
      mitigation: d.get("mitigation"),
      reviewDate: d.get("reviewDate"),
      status: "open",
    });
    setCreate(false);
    load();
  }
  return (
    <section className="card domain-panel">
      <div className="card-head">
        <div>
          <h2>Supplier Risk</h2>
          <p>재무·운영·보안·준법·공급망·품질·계약 위험을 지속 관리합니다.</p>
        </div>
        <button className="button" onClick={() => setCreate(true)}>
          <Plus />
          리스크 등록
        </button>
      </div>
      <div className="domain-list">
        {items.map((item, index) => (
          <article key={String(item.id || index)}>
            <span className="object-icon">
              <ShieldAlert />
            </span>
            <div>
              <b>{String(item.riskType)}</b>
              <small>{String(item.description || "설명 없음")}</small>
            </div>
            <strong>{Number(item.score || 0).toFixed(1)}</strong>
            <RiskBadge level={String(item.severity)} />
          </article>
        ))}
      </div>
      {!items.length && (
        <Empty
          title="등록된 리스크가 없습니다"
          description="현재 확인된 위험 신호가 없습니다."
        />
      )}
      {create && (
        <Modal
          title="공급업체 리스크 등록"
          description="발생가능성과 영향도에 따라 위험점수를 관리합니다."
          onClose={() => setCreate(false)}
        >
          <form onSubmit={submit}>
            <div className="form-grid">
              <Field label="Risk 유형">
                <select name="riskType">
                  <option>Financial</option>
                  <option>Operational</option>
                  <option>Security</option>
                  <option>Compliance</option>
                  <option>Supply Chain</option>
                  <option>Quality</option>
                  <option>Contract</option>
                </select>
              </Field>
              <Field label="위험 단계">
                <select name="severity">
                  <option>LOW</option>
                  <option>MEDIUM</option>
                  <option>HIGH</option>
                  <option>CRITICAL</option>
                </select>
              </Field>
              <Field label="발생가능성 (0~10)">
                <input
                  name="probability"
                  type="number"
                  min="0"
                  max="10"
                  required
                />
              </Field>
              <Field label="영향도 (0~10)">
                <input name="impact" type="number" min="0" max="10" required />
              </Field>
              <Field label="검토일">
                <input name="reviewDate" type="date" />
              </Field>
              <Field label="위험 설명">
                <textarea name="description" rows={3} />
              </Field>
              <Field label="완화 계획">
                <textarea name="mitigation" rows={3} />
              </Field>
            </div>
            <div className="form-actions">
              <button
                type="button"
                className="button secondary"
                onClick={() => setCreate(false)}
              >
                취소
              </button>
              <button className="button">리스크 저장</button>
            </div>
          </form>
        </Modal>
      )}
    </section>
  );
}

type ScreeningTemplate = {
  id: string;
  name: string;
  active: boolean;
  items: { code: string; name: string; weight: number; required: boolean }[];
  requiredDocumentTypes: string[];
};
type Screening = {
  id: string;
  templateId: string;
  templateName: string;
  status: string;
  responses: Record<string, number>;
  domainResults: Record<string, { score: number; weightedScore: number }>;
  result?: string;
  comments?: string;
  completedAt?: string;
  createdAt: string;
};

function ScreeningPanel({ supplierId }: { supplierId: string }) {
  const [templates, setTemplates] = useState<ScreeningTemplate[]>();
  const [screenings, setScreenings] = useState<Screening[]>();
  const [selectedTemplate, setSelectedTemplate] = useState("");
  const [scores, setScores] = useState<Record<string, number>>({});
  const [comments, setComments] = useState("");
  const [resultNotice, setResultNotice] = useState("");
  const load = useCallback(
    () =>
      Promise.all([
        api<{ items: ScreeningTemplate[] }>("/api/v1/screening-templates"),
        api<{ items: Screening[] }>(
          `/api/v1/suppliers/${supplierId}/screenings`,
        ),
      ]).then(([t, s]) => {
        setTemplates(t.items);
        setScreenings(s.items);
        if (!selectedTemplate && t.items[0]) setSelectedTemplate(t.items[0].id);
      }),
    [supplierId, selectedTemplate],
  );
  useEffect(() => {
    void load();
  }, [load]);
  if (!templates || !screenings) return <Loading />;
  const active = screenings.find((x) => x.status !== "completed");
  const template = templates.find(
    (x) => x.id === (active?.templateId || selectedTemplate),
  );
  async function start() {
    await post(`/api/v1/suppliers/${supplierId}/screenings`, {
      templateId: selectedTemplate,
    });
    await load();
  }
  async function save(complete: boolean) {
    if (!active) return;
    const response = await patch<{
      result?: string;
      totalScore: number;
      missingDocumentTypes?: string[];
    }>(`/api/v1/screenings/${active.id}`, {
      responses: { ...(active.responses || {}), ...scores },
      comments,
      complete,
    });
    const missing = response.missingDocumentTypes?.length
      ? ` · 필수문서 누락: ${response.missingDocumentTypes.join(", ")}`
      : "";
    setResultNotice(
      `${complete ? response.result : "임시 저장"} · ${response.totalScore.toFixed(1)}점${missing}`,
    );
    await load();
  }
  return (
    <div className="screening-layout">
      <section className="card screening-workbench">
        <div className="card-head">
          <div>
            <h2>공급업체 종합심사</h2>
            <p>재무·보안·준법·품질을 관리자 정의 기준으로 판정합니다.</p>
          </div>
          {active && <Badge tone="warning">진행 중</Badge>}
        </div>
        {!active ? (
          <div className="screening-start">
            <ClipboardCheck />
            <div>
              <h3>새 심사 시작</h3>
              <p>
                필수 문서 보유 여부와 항목별 점수를 함께 검사하여 PASS,
                CONDITIONAL_PASS, REVIEW_REQUIRED 또는 REJECT로 판정합니다.
              </p>
            </div>
            <select
              value={selectedTemplate}
              onChange={(e) => setSelectedTemplate(e.target.value)}
            >
              {templates
                .filter((x) => x.active)
                .map((x) => (
                  <option value={x.id} key={x.id}>
                    {x.name}
                  </option>
                ))}
            </select>
            <button
              className="button"
              onClick={start}
              disabled={!selectedTemplate}
            >
              심사 시작
            </button>
          </div>
        ) : (
          <div className="screening-form">
            <div className="screening-template-head">
              <div>
                <b>{active.templateName}</b>
                <small>
                  필수 문서:{" "}
                  {template?.requiredDocumentTypes.join(", ") || "없음"}
                </small>
              </div>
            </div>
            <div className="screening-criteria">
              {template?.items.map((item) => (
                <label key={item.code}>
                  <span>
                    <b>{item.name}</b>
                    <small>
                      가중치 {item.weight}% {item.required ? "· 필수" : ""}
                    </small>
                  </span>
                  <input
                    type="number"
                    min="0"
                    max="100"
                    value={
                      scores[item.code] ?? active.responses?.[item.code] ?? ""
                    }
                    onChange={(e) =>
                      setScores((current) => ({
                        ...current,
                        [item.code]: Number(e.target.value),
                      }))
                    }
                    required={item.required}
                  />
                  <em>/ 100</em>
                </label>
              ))}
            </div>
            <Field label="심사 의견">
              <textarea
                rows={3}
                value={comments || active.comments || ""}
                onChange={(e) => setComments(e.target.value)}
              />
            </Field>
            {resultNotice && (
              <div className="security-banner">{resultNotice}</div>
            )}
            <div className="form-actions">
              <button className="button secondary" onClick={() => save(false)}>
                임시 저장
              </button>
              <button className="button" onClick={() => save(true)}>
                심사 완료 · 판정
              </button>
            </div>
          </div>
        )}
      </section>
      <section className="card screening-history">
        <div className="card-head">
          <h2>심사 이력</h2>
          <Badge>{screenings.length}</Badge>
        </div>
        {screenings.map((item) => (
          <div key={item.id}>
            <span className="object-icon">
              <ClipboardCheck />
            </span>
            <span className="stack">
              <b>{item.templateName}</b>
              <small>{date(item.completedAt || item.createdAt)}</small>
            </span>
            <Badge tone={statusTone(item.result || item.status)}>
              {item.result || item.status}
            </Badge>
          </div>
        ))}
        {!screenings.length && (
          <Empty
            title="심사 이력이 없습니다"
            description="첫 심사를 시작하세요."
          />
        )}
      </section>
    </div>
  );
}

type SupplierDocument = {
  id: string;
  documentType: string;
  name: string;
  version: number;
  contentType?: string;
  size: number;
  checksum: string;
  expiresAt?: string;
  status: string;
  createdAt: string;
};
function DocumentsPanel({ supplierId }: { supplierId: string }) {
  const [items, setItems] = useState<SupplierDocument[]>();
  const [detail, setDetail] = useState<{
    document: SupplierDocument;
    signatures: Record<string, unknown>[];
  }>();
  const load = useCallback(
    () =>
      api<{ items: SupplierDocument[] }>(
        `/api/v1/documents?supplierId=${supplierId}`,
      ).then((x) => setItems(x.items)),
    [supplierId],
  );
  useEffect(() => {
    void load();
  }, [load]);
  if (!items) return <Loading />;
  async function inspect(document: SupplierDocument) {
    const signatures = await api<{ items: Record<string, unknown>[] }>(
      `/api/v1/documents/${document.id}/signatures`,
    );
    setDetail({ document, signatures: signatures.items });
  }
  async function sign(document: SupplierDocument) {
    await post(`/api/v1/documents/${document.id}/signatures`, {
      signatureType: "approval",
      meaning: "본 문서의 내용과 체크섬을 확인하고 승인함",
      comment: "Vendra 전자서명",
    });
    await inspect(document);
    load();
  }
  return (
    <section className="card domain-panel">
      <div className="card-head">
        <div>
          <h2>통합 문서관리</h2>
          <p>
            버전, 만료일, SHA-256 Checksum, 접근로그와 전자서명을 보존합니다.
          </p>
        </div>
        <Badge>{items.length}</Badge>
      </div>
      <div className="document-list">
        {items.map((document) => (
          <article key={document.id}>
            <span className="object-icon">
              <FileText />
            </span>
            <div>
              <b>{document.name}</b>
              <small>
                {document.documentType} · v{document.version} ·{" "}
                {Math.ceil(document.size / 1024)} KB
              </small>
            </div>
            <Badge tone={statusTone(document.status)}>{document.status}</Badge>
            <span className="document-actions">
              <button
                className="button secondary compact"
                onClick={() =>
                  window.open(
                    `/api/v1/documents/${document.id}/preview`,
                    "_blank",
                  )
                }
              >
                미리보기
              </button>
              <a
                className="button secondary compact"
                href={`/api/v1/documents/${document.id}/download`}
              >
                <Download />
                다운로드
              </a>
              <button
                className="button compact"
                onClick={() => inspect(document)}
              >
                서명 · 상세
              </button>
            </span>
          </article>
        ))}
      </div>
      {!items.length && (
        <Empty
          title="등록된 문서가 없습니다"
          description="상단의 문서 등록으로 첫 문서를 업로드하세요."
        />
      )}
      {detail && (
        <Modal
          title={detail.document.name}
          description={`v${detail.document.version} · SHA-256 ${detail.document.checksum}`}
          onClose={() => setDetail(undefined)}
        >
          <dl className="document-metadata">
            <dt>문서 유형</dt>
            <dd>{detail.document.documentType}</dd>
            <dt>상태</dt>
            <dd>{detail.document.status}</dd>
            <dt>만료일</dt>
            <dd>{date(detail.document.expiresAt)}</dd>
            <dt>Checksum</dt>
            <dd>
              <code>{detail.document.checksum}</code>
            </dd>
          </dl>
          <h3>전자서명</h3>
          {detail.signatures.map((signature, index) => (
            <div className="signature-row" key={String(signature.id || index)}>
              <ShieldAlert />
              <span>
                <b>{String(signature.signerName)}</b>
                <small>
                  {String(signature.signatureType)} ·{" "}
                  {date(String(signature.signedAt))}
                </small>
              </span>
            </div>
          ))}
          {!detail.signatures.length && (
            <p className="muted-copy">등록된 서명이 없습니다.</p>
          )}
          <div className="form-actions">
            <button className="button" onClick={() => sign(detail.document)}>
              <ClipboardCheck />
              문서 확인 · 전자서명
            </button>
          </div>
        </Modal>
      )}
    </section>
  );
}

function RelatedList({
  items,
  label,
}: {
  items: BusinessObject[];
  label: string;
}) {
  return (
    <div className="data-card">
      {items.length ? (
        <table>
          <thead>
            <tr>
              <th>번호</th>
              <th>제목</th>
              <th>상태</th>
              <th>금액</th>
              <th>기한</th>
            </tr>
          </thead>
          <tbody>
            {items.map((o) => (
              <tr key={o.id}>
                <td>{o.number}</td>
                <td>
                  <b>{o.title}</b>
                </td>
                <td>
                  <Badge tone={statusTone(o.status)}>{o.status}</Badge>
                </td>
                <td>{money(o.amount)}</td>
                <td>{date(o.endDate || o.dueDate)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <Empty
          title={`${label} 데이터가 없습니다`}
          description="새 업무 데이터가 연결되면 이곳에 표시됩니다."
        />
      )}
    </div>
  );
}
function AsyncSub({ endpoint, empty }: { endpoint: string; empty: string }) {
  const [items, setItems] = useState<Record<string, unknown>[]>();
  useEffect(() => {
    api<{ items: Record<string, unknown>[] }>(endpoint).then((x) =>
      setItems(x.items),
    );
  }, [endpoint]);
  if (!items) return <Loading />;
  if (!items.length)
    return (
      <Empty
        title={empty}
        description="권한이 있는 사용자가 새 데이터를 등록할 수 있습니다."
      />
    );
  return (
    <div className="data-card json-list">
      {items.map((x, i) => (
        <div key={String(x.id || i)}>
          {Object.entries(x)
            .filter(([k]) => !["id", "createdAt", "updatedAt"].includes(k))
            .slice(0, 6)
            .map(([k, v]) => (
              <span key={k}>
                <small>{k}</small>
                <b>
                  {typeof v === "object" ? JSON.stringify(v) : String(v ?? "—")}
                </b>
              </span>
            ))}
        </div>
      ))}
    </div>
  );
}
function Metric({ label, value }: { label: string; value: number }) {
  return (
    <div>
      <span>
        <b>{label}</b>
        <em>{value.toFixed(1)}%</em>
      </span>
      <i>
        <b style={{ width: `${Math.min(100, value)}%` }} />
      </i>
    </div>
  );
}
function statusLabel(s: string) {
  return (
    (
      {
        candidate: "후보",
        registration: "등록",
        screening: "심사",
        approved: "승인",
        active: "거래 가능",
        improvement: "개선 대상",
        suspended: "거래 중단",
      } as Record<string, string>
    )[s] || s
  );
}
