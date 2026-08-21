import { useCallback, useEffect, useState } from "react";
import {
  Check,
  CheckCircle2,
  Clock3,
  FileCheck2,
  ListChecks,
  RotateCcw,
  X,
} from "lucide-react";
import { useSearchParams } from "react-router-dom";
import { api, date, money, post } from "../api";
import { Badge, Empty, Loading, Modal, PageHeader } from "../components";
import { statusTone } from "../status";

type Approval = {
  id: string;
  objectType: string;
  objectId: string;
  status: string;
  currentStep: number;
  currentStepDefinition: { name?: string; role?: string };
  requestedBy?: string;
  createdAt: string;
  workflowName: string;
  number?: string;
  title?: string;
  amount?: number;
  supplierName?: string;
};

export default function Approvals() {
  const [params, setParams] = useSearchParams();
  const [items, setItems] = useState<Approval[]>();
  const [selected, setSelected] = useState<Approval>();
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [bulkAction, setBulkAction] = useState<string>();
  const [bulkComment, setBulkComment] = useState("");
  const [bulkBusy, setBulkBusy] = useState(false);
  const [message, setMessage] = useState("");
  const load = useCallback(
    () =>
      api<{ items: Approval[] }>("/api/v1/approvals").then((response) => {
        setItems(response.items);
        const requested = params.get("selected");
        if (requested) setSelected(response.items.find((item) => item.id === requested));
        setChecked((current) => {
          const available = new Set(response.items.map((item) => item.id));
          return new Set([...current].filter((id) => available.has(id)));
        });
      }),
    [params],
  );
  useEffect(() => {
    void load();
  }, [load]);

  function choose(item: Approval) {
    setSelected(item);
    const next = new URLSearchParams(params);
    next.set("selected", item.id);
    setParams(next, { replace: true });
  }

  function toggle(id: string) {
    setChecked((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  async function submitBulk() {
    if (!bulkAction || !checked.size) return;
    setBulkBusy(true);
    setMessage("");
    const results = await Promise.allSettled(
      [...checked].map((id) =>
        post(`/api/v1/approvals/${id}/actions`, {
          action: bulkAction,
          comment: bulkComment,
        }),
      ),
    );
    const succeeded = results.filter((result) => result.status === "fulfilled").length;
    const failed = results.length - succeeded;
    setMessage(failed ? `${succeeded}건 처리, ${failed}건은 권한 또는 상태를 확인해 주세요.` : `${succeeded}건을 처리했습니다.`);
    setBulkBusy(false);
    setBulkAction(undefined);
    setBulkComment("");
    setChecked(new Set());
    setSelected(undefined);
    await load();
  }

  if (!items) return <Loading />;
  return (
    <div className="page">
      <PageHeader
        eyebrow="Approval workspace"
        title="내 승인함"
        description="업무 맥락과 공급업체 리스크를 확인하고 한 건 또는 여러 건을 안전하게 처리합니다."
      />
      <div className="approval-summary">
        <div><Clock3 /><span><b>{items.length}</b>승인 대기</span></div>
        <div><ListChecks /><span><b>{checked.size}</b>선택됨</span></div>
        <div><CheckCircle2 /><span><b>{items.length ? "진행" : "완료"}</b>현재 상태</span></div>
      </div>
      {message && <div className="approval-result"><CheckCircle2 />{message}</div>}
      {checked.size > 0 && (
        <div className="approval-bulk-bar">
          <span><b>{checked.size}건</b> 일괄 결정</span>
          <button className="button ghost" onClick={() => setChecked(new Set())}>선택 해제</button>
          <button className="button secondary" onClick={() => setBulkAction("return")}><RotateCcw />보완 요청</button>
          <button className="button danger" onClick={() => setBulkAction("reject")}><X />반려</button>
          <button className="button" onClick={() => setBulkAction("approve")}><Check />승인</button>
        </div>
      )}
      {items.length ? (
        <div className="approval-layout">
          <div className="approval-list">
            <label className="approval-select-all">
              <input
                type="checkbox"
                checked={items.length > 0 && items.every((item) => checked.has(item.id))}
                onChange={(event) => setChecked(event.target.checked ? new Set(items.map((item) => item.id)) : new Set())}
              />
              현재 승인 요청 전체 선택
            </label>
            {items.map((item) => (
              <div className={`approval-list-row ${selected?.id === item.id ? "active" : ""}`} key={item.id}>
                <label className="approval-check" title="일괄 처리 선택">
                  <input type="checkbox" checked={checked.has(item.id)} onChange={() => toggle(item.id)} aria-label={`${item.title || item.number} 선택`} />
                </label>
                <button onClick={() => choose(item)}>
                  <span className="object-icon"><FileCheck2 /></span>
                  <div>
                    <span><Badge tone={statusTone(item.status)}>{item.currentStepDefinition?.name || "승인 대기"}</Badge><small>{date(item.createdAt)}</small></span>
                    <b>{item.title || item.number}</b>
                    <p>{item.supplierName || "공급업체 미지정"} · {money(item.amount)}</p>
                  </div>
                </button>
              </div>
            ))}
          </div>
          <div className="approval-preview">
            {selected ? (
              <ApprovalDetail item={selected} onDone={() => { setSelected(undefined); void load(); }} />
            ) : (
              <Empty title="검토할 업무를 선택하세요" description="왼쪽 목록에서 승인 요청을 선택하면 상세 맥락을 표시합니다." />
            )}
          </div>
        </div>
      ) : (
        <Empty icon={<CheckCircle2 />} title="모든 승인 업무를 처리했습니다" description="현재 사용자에게 할당된 승인 대기 건이 없습니다." />
      )}
      {bulkAction && (
        <Modal title={`${checked.size}건을 ${actionLabel(bulkAction)}하시겠습니까?`} description="각 승인 건은 현재 권한과 상태를 서버에서 다시 검증합니다." onClose={() => setBulkAction(undefined)}>
          <label className="field"><span>공통 검토 의견 {bulkAction !== "approve" && <em>필수 권장</em>}</span><textarea rows={5} value={bulkComment} onChange={(event) => setBulkComment(event.target.value)} placeholder="일괄 결정 사유 또는 보완 요청사항을 남기세요." /></label>
          <div className="form-actions">
            <button className="button secondary" onClick={() => setBulkAction(undefined)}>취소</button>
            <button className={`button ${bulkAction === "reject" ? "danger" : ""}`} disabled={bulkBusy || (bulkAction !== "approve" && !bulkComment.trim())} onClick={() => void submitBulk()}>{bulkBusy ? "처리 중…" : "일괄 결정 확정"}</button>
          </div>
        </Modal>
      )}
    </div>
  );
}

function ApprovalDetail({ item, onDone }: { item: Approval; onDone: () => void }) {
  const [action, setAction] = useState<string>();
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit() {
    if (!action) return;
    setBusy(true);
    try {
      await post(`/api/v1/approvals/${item.id}/actions`, { action, comment });
      onDone();
    } finally {
      setBusy(false);
    }
  }
  return <>
    <div className="approval-detail-head"><div><p className="eyebrow">{item.workflowName}</p><h2>{item.title}</h2><span>{item.number} · {item.objectType}</span></div><Badge tone="warning">{item.currentStepDefinition.name || `단계 ${item.currentStep + 1}`}</Badge></div>
    <dl className="approval-data"><dt>공급업체</dt><dd>{item.supplierName || "—"}</dd><dt>금액</dt><dd>{money(item.amount)}</dd><dt>요청일</dt><dd>{date(item.createdAt)}</dd><dt>승인 역할</dt><dd>{item.currentStepDefinition.role || "지정 사용자"}</dd></dl>
    <div className="approval-context"><h3>검토 체크포인트</h3><p><Check />공급업체 거래상태 및 Risk 등급</p><p><Check />예산과 계약금액의 정합성</p><p><Check />첨부 문서 및 계약 조건</p></div>
    <div className="approval-actions"><button className="button secondary" onClick={() => setAction("return")}><RotateCcw />보완 요청</button><button className="button danger" onClick={() => setAction("reject")}><X />반려</button><button className="button" onClick={() => setAction("approve")}><Check />승인</button></div>
    {action && <Modal title={`${actionLabel(action)}하시겠습니까?`} onClose={() => setAction(undefined)}><label className="field"><span>검토 의견</span><textarea rows={5} value={comment} onChange={(event) => setComment(event.target.value)} placeholder="결정 사유 또는 요청사항을 남기세요." /></label><div className="form-actions"><button className="button secondary" onClick={() => setAction(undefined)}>취소</button><button className={`button ${action === "reject" ? "danger" : ""}`} disabled={busy || (action !== "approve" && !comment.trim())} onClick={() => void submit()}>{busy ? "처리 중…" : "결정 확정"}</button></div></Modal>}
  </>;
}

function actionLabel(action: string) {
  return action === "approve" ? "승인" : action === "reject" ? "반려" : "보완 요청";
}
