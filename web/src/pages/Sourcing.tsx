import { FormEvent, useCallback, useEffect, useState } from "react";
import {
  ArrowLeft,
  CheckCircle2,
  Gavel,
  MessageSquare,
  Plus,
  Send,
  Users,
} from "lucide-react";
import { Link, useParams } from "react-router-dom";
import { api, date, money, post } from "../api";
import {
  Badge,
  Empty,
  Field,
  Loading,
  Modal,
  PageHeader,
  RiskBadge,
  statusTone,
} from "../components";
import { BusinessObject, Supplier } from "../types";

type Participant = {
  id: string;
  supplierId: string;
  supplierName: string;
  status: string;
  invitedAt: string;
  viewedAt?: string;
};
type Comparison = {
  responseId: string;
  supplierId: string;
  supplierName: string;
  status: string;
  totalAmount?: number;
  deliveryDays?: number;
  warranty?: string;
  priceScore?: number;
  qualityScore?: number;
  deliveryScore?: number;
  riskScore?: number;
  technicalScore?: number;
  finalScore?: number;
  supplierRisk: string;
  supplierGrade?: string;
};
type CommitteeMember = {
  userId: string;
  displayName: string;
  email: string;
  role: string;
  appointedAt: string;
};
export default function SourcingWorkspace() {
  const { type = "rfq", id } = useParams();
  const endpoint = type === "rfp" ? "/api/v1/rfp" : "/api/v1/rfq";
  const [object, setObject] = useState<BusinessObject>();
  const [participants, setParticipants] = useState<Participant[]>([]);
  const [comparison, setComparison] = useState<Comparison[]>([]);
  const [committee, setCommittee] = useState<CommitteeMember[]>([]);
  const [invite, setInvite] = useState(false);
  const [appoint, setAppoint] = useState(false);
  async function evaluate(response: Comparison) {
    const technical = window.prompt(
      `${response.supplierName} 기술평가 점수(0~100)`,
      String(response.technicalScore || 80),
    );
    if (technical === null) return;
    const fit = window.prompt("요구사항 적합도 점수(0~100)", "80");
    if (fit === null) return;
    await post(`/api/v1/sourcing/responses/${response.responseId}/evaluate`, {
      scores: { technical: Number(technical), fit: Number(fit) },
      comment: "Sourcing Workspace 평가",
    });
    load();
  }
  async function select(
    response: Comparison,
    selectionType: "preferred" | "final",
  ) {
    const label =
      selectionType === "preferred" ? "우선협상대상" : "최종 공급업체";
    const reason = window.prompt(
      `${response.supplierName}을(를) ${label}으로 선정하는 사유`,
    );
    if (reason === null) return;
    await post(`/api/v1/sourcing/${id}/select`, {
      responseId: response.responseId,
      selectionType,
      reason,
    });
    load();
  }
  const load = useCallback(
    () =>
      Promise.all([
        api<BusinessObject>(`${endpoint}/${id}`),
        api<{ items: Participant[] }>(`/api/v1/sourcing/${id}/participants`),
        api<{ items: Comparison[] }>(`/api/v1/sourcing/${id}/comparison`),
        api<{ items: CommitteeMember[] }>(`/api/v1/sourcing/${id}/committee`),
      ]).then(([o, p, c, m]) => {
        setObject(o);
        setParticipants(p.items);
        setComparison(c.items);
        setCommittee(m.items);
      }),
    [endpoint, id],
  );
  useEffect(() => {
    load();
  }, [load]);
  if (!object) return <Loading />;
  return (
    <div className="page">
      <Link className="back-link" to={type === "rfp" ? "/rfp" : "/rfq"}>
        <ArrowLeft />
        {type.toUpperCase()} 목록
      </Link>
      <PageHeader
        eyebrow="Strategic sourcing workspace"
        title={object.title}
        description={`${object.number} · 제출 마감 ${date(object.dueDate)}`}
        actions={
          <>
            <button
              className="button secondary"
              onClick={() => setAppoint(true)}
            >
              <Gavel />
              평가위원 지정
            </button>
            <button className="button" onClick={() => setInvite(true)}>
              <Plus />
              공급업체 초대
            </button>
          </>
        }
      />
      <section className="sourcing-summary">
        <div>
          <span>상태</span>
          <Badge tone={statusTone(object.status)}>{object.status}</Badge>
        </div>
        <div>
          <span>초대 업체</span>
          <strong>{participants.length}</strong>
        </div>
        <div>
          <span>제출 완료</span>
          <strong>
            {comparison.filter((x) => x.status === "submitted").length}
          </strong>
        </div>
        <div>
          <span>마감일</span>
          <strong>{date(object.dueDate)}</strong>
        </div>
      </section>
      <div className="sourcing-grid">
        <div className="card">
          <div className="card-head">
            <h2>종합 비교 Matrix</h2>
            <Badge tone="purple">가격 + 품질 + 납기 + 위험 + 기술</Badge>
          </div>
          {comparison.length ? (
            <div className="comparison-scroll">
              <table>
                <thead>
                  <tr>
                    <th>공급업체</th>
                    <th>총금액</th>
                    <th>납기</th>
                    <th>보증</th>
                    <th>가격</th>
                    <th>품질</th>
                    <th>납기</th>
                    <th>Risk</th>
                    <th>기술</th>
                    <th>종합</th>
                    <th>평가 · 선정</th>
                  </tr>
                </thead>
                <tbody>
                  {comparison.map((x, i) => (
                    <tr
                      className={i === 0 ? "recommended" : ""}
                      key={x.responseId}
                    >
                      <td>
                        <span className="stack">
                          <b>{x.supplierName}</b>
                          <small>
                            {x.supplierGrade || "미평가"} · {x.supplierRisk}
                          </small>
                        </span>
                      </td>
                      <td>{money(x.totalAmount)}</td>
                      <td>{x.deliveryDays ? `${x.deliveryDays}일` : "—"}</td>
                      <td>{x.warranty || "—"}</td>
                      <Score value={x.priceScore} />
                      <Score value={x.qualityScore} />
                      <Score value={x.deliveryScore} />
                      <Score value={x.riskScore} />
                      <Score value={x.technicalScore} />
                      <td>
                        <strong className="final-score">
                          {x.finalScore?.toFixed(1) || "—"}
                        </strong>
                        {i === 0 && <Badge tone="success">추천</Badge>}
                      </td>
                      <td>
                        <div className="matrix-actions">
                          <button
                            className="icon-button"
                            title="평가위원 점수 입력"
                            onClick={() => evaluate(x)}
                          >
                            <Gavel />
                          </button>
                          <button
                            className="icon-button"
                            title="우선협상대상 선정"
                            onClick={() => select(x, "preferred")}
                          >
                            <CheckCircle2 />
                          </button>
                          <button
                            className="button compact"
                            onClick={() => select(x, "final")}
                          >
                            최종 선정
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <Empty
              title="제출된 응답이 없습니다"
              description="공급업체가 견적 또는 제안서를 제출하면 자동 비교점수를 표시합니다."
            />
          )}
        </div>
        <div className="card participants">
          <div className="card-head">
            <h2>참여 공급업체</h2>
          </div>
          {participants.map((p) => (
            <div key={p.id}>
              <span className="company-avatar">
                {p.supplierName.slice(0, 2)}
              </span>
              <div>
                <b>{p.supplierName}</b>
                <small>초대 {date(p.invitedAt)}</small>
              </div>
              <Badge tone={statusTone(p.status)}>{p.status}</Badge>
            </div>
          ))}
          {!participants.length && (
            <Empty
              icon={<Users />}
              title="초대 업체가 없습니다"
              description="거래 가능한 공급업체를 초대하세요."
            />
          )}
        </div>
      </div>
      <SourcingQuestions sourcingId={object.id} />
      <section className="card sourcing-committee">
        <div className="card-head">
          <div>
            <h2>평가위원회</h2>
            <small>지정된 위원만 기술·적합도 평가를 제출할 수 있습니다.</small>
          </div>
          <Badge>{committee.length}명</Badge>
        </div>
        <div className="committee-list">
          {committee.map((member) => (
            <div key={member.userId}>
              <span className="avatar">{member.displayName.slice(0, 1)}</span>
              <div>
                <b>{member.displayName}</b>
                <small>{member.email}</small>
              </div>
              <Badge>{member.role}</Badge>
            </div>
          ))}
        </div>
        {!committee.length && (
          <Empty
            icon={<Gavel />}
            title="평가위원이 지정되지 않았습니다"
            description="위원 미지정 상태에서는 RFQ/RFP 수정 권한 보유자가 평가할 수 있습니다."
          />
        )}
      </section>
      {invite && (
        <InviteSourcing
          sourcingId={object.id}
          current={participants.map((x) => x.supplierId)}
          onClose={() => setInvite(false)}
          onSaved={() => {
            setInvite(false);
            load();
          }}
        />
      )}
      {appoint && (
        <AppointCommittee
          sourcingId={object.id}
          current={committee.map((member) => member.userId)}
          onClose={() => setAppoint(false)}
          onSaved={() => {
            setAppoint(false);
            load();
          }}
        />
      )}
    </div>
  );
}

type Candidate = {
  id: string;
  displayName: string;
  email: string;
  roles: string[];
};
function AppointCommittee({
  sourcingId,
  current,
  onClose,
  onSaved,
}: {
  sourcingId: string;
  current: string[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [items, setItems] = useState<Candidate[]>();
  const [selected, setSelected] = useState<string[]>([]);
  useEffect(() => {
    api<{ items: Candidate[] }>("/api/v1/sourcing-committee-candidates").then(
      (x) => setItems(x.items.filter((item) => !current.includes(item.id))),
    );
  }, [current]);
  async function submit(e: FormEvent) {
    e.preventDefault();
    await post(`/api/v1/sourcing/${sourcingId}/committee`, {
      userIds: selected,
      role: "evaluator",
    });
    onSaved();
  }
  return (
    <Modal
      title="RFQ/RFP 평가위원 지정"
      description="조직 범위 내 내부 사용자에게 기술·가격 평가 역할을 부여합니다."
      onClose={onClose}
    >
      <form onSubmit={submit}>
        <div className="candidate-list">
          {items?.map((item) => (
            <label key={item.id}>
              <input
                type="checkbox"
                checked={selected.includes(item.id)}
                onChange={() =>
                  setSelected((value) =>
                    value.includes(item.id)
                      ? value.filter((id) => id !== item.id)
                      : [...value, item.id],
                  )
                }
              />
              <span className="avatar">{item.displayName.slice(0, 1)}</span>
              <span>
                <b>{item.displayName}</b>
                <small>
                  {item.email} · {item.roles.join(", ")}
                </small>
              </span>
            </label>
          ))}
        </div>
        {items && !items.length && (
          <Empty
            title="추가할 평가위원이 없습니다"
            description="사용자·권한 관리에서 내부 사용자를 추가하세요."
          />
        )}
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button" disabled={!selected.length}>
            위원 지정
          </button>
        </div>
      </form>
    </Modal>
  );
}

type SourcingQuestion = {
  id: string;
  supplierName?: string;
  askedBy: string;
  question: string;
  answer?: string;
  answeredBy?: string;
  visibility: string;
  askedAt: string;
};
function SourcingQuestions({ sourcingId }: { sourcingId: string }) {
  const [items, setItems] = useState<SourcingQuestion[]>();
  const [question, setQuestion] = useState("");
  const load = useCallback(
    () =>
      api<{ items: SourcingQuestion[] }>(
        `/api/v1/sourcing/${sourcingId}/questions`,
      ).then((x) => setItems(x.items)),
    [sourcingId],
  );
  useEffect(() => {
    void load();
  }, [load]);
  async function ask(e: FormEvent) {
    e.preventDefault();
    await post(`/api/v1/sourcing/${sourcingId}/questions`, {
      question,
      visibility: "participants",
    });
    setQuestion("");
    load();
  }
  async function answer(item: SourcingQuestion) {
    const value = window.prompt("공급업체에 전달할 답변", item.answer || "");
    if (!value) return;
    await api(`/api/v1/sourcing/questions/${item.id}/answer`, {
      method: "PATCH",
      body: JSON.stringify({ answer: value }),
    });
    load();
  }
  return (
    <section className="card sourcing-questions">
      <div className="card-head">
        <div>
          <h2>질의응답</h2>
          <p>전체 참여업체에 공개되는 공식 질의응답을 관리합니다.</p>
        </div>
        <Badge>{items?.length || 0}</Badge>
      </div>
      <form onSubmit={ask}>
        <MessageSquare />
        <input
          value={question}
          onChange={(e) => setQuestion(e.target.value)}
          placeholder="참여업체에 공지할 질문 또는 안내"
          required
        />
        <button className="button">등록</button>
      </form>
      {items?.map((item) => (
        <article key={item.id}>
          <header>
            <b>{item.supplierName || item.askedBy}</b>
            <Badge tone={item.visibility === "private" ? "warning" : "neutral"}>
              {item.visibility}
            </Badge>
            <small>{date(item.askedAt)}</small>
          </header>
          <p>{item.question}</p>
          {item.answer ? (
            <blockquote>{item.answer}</blockquote>
          ) : item.supplierName ? (
            <button
              className="button secondary compact"
              onClick={() => answer(item)}
            >
              답변 등록
            </button>
          ) : null}
        </article>
      ))}
      {items && !items.length && (
        <Empty
          title="등록된 질의응답이 없습니다"
          description="첫 안내를 등록하세요."
        />
      )}
    </section>
  );
}
function Score({ value }: { value?: number }) {
  return (
    <td>
      <span
        className={`matrix-score ${(value || 0) >= 80 ? "good" : (value || 0) < 50 ? "bad" : ""}`}
      >
        {value?.toFixed(0) || "—"}
      </span>
    </td>
  );
}
function InviteSourcing({
  sourcingId,
  current,
  onClose,
  onSaved,
}: {
  sourcingId: string;
  current: string[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [suppliers, setSuppliers] = useState<Supplier[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  useEffect(() => {
    api<{ items: Supplier[] }>(
      "/api/v1/suppliers?status=active&limit=500",
    ).then((x) => setSuppliers(x.items.filter((s) => !current.includes(s.id))));
  }, [current]);
  async function submit(e: FormEvent) {
    e.preventDefault();
    await post(`/api/v1/sourcing/${sourcingId}/participants`, {
      supplierIds: selected,
    });
    onSaved();
  }
  return (
    <Modal
      title="공급업체 초대"
      description="복수 공급업체를 선택해 견적 또는 제안 참여를 요청합니다."
      onClose={onClose}
    >
      <form onSubmit={submit}>
        <div className="invite-list">
          {suppliers.map((s) => (
            <label key={s.id}>
              <input
                type="checkbox"
                checked={selected.includes(s.id)}
                onChange={() =>
                  setSelected((x) =>
                    x.includes(s.id)
                      ? x.filter((i) => i !== s.id)
                      : [...x, s.id],
                  )
                }
              />
              <span className="company-avatar">{s.name.slice(0, 2)}</span>
              <span>
                <b>{s.name}</b>
                <small>
                  {s.grade || "미평가"} · {s.riskLevel}
                </small>
              </span>
              <RiskBadge level={s.riskLevel} />
            </label>
          ))}
        </div>
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button" disabled={!selected.length}>
            <Send />
            {selected.length}개 업체 초대
          </button>
        </div>
      </form>
    </Modal>
  );
}
