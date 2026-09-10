export type StatusTone =
  | "neutral"
  | "success"
  | "warning"
  | "danger"
  | "info"
  | "purple";

// supplierStatuses is the 거래 상태 vocabulary, in the order a supplier moves
// through it. It is the same list the API holds in supplierStatuses, and it is
// listed once here because it had been written out three times — the register's
// filter, the edit form's dropdown and the label map — and the three had
// already drifted apart. The edit form said "registered" where everything else
// said "registration", so choosing 등록 there saved a status the filter never
// matches and the label map has no word for; and since "registration" was
// missing from its options, opening a supplier that had signed itself up
// through the portal and pressing save quietly demoted it to 후보, because a
// select whose value is not among its options shows the first one.
export const supplierStatuses: { value: string; label: string }[] = [
  { value: "candidate", label: "후보" },
  { value: "registration", label: "등록" },
  { value: "screening", label: "심사" },
  { value: "approved", label: "승인" },
  { value: "active", label: "거래 가능" },
  { value: "preferred", label: "우수" },
  { value: "improvement", label: "개선 대상" },
  { value: "suspended", label: "거래 중단" },
  { value: "terminated", label: "거래 종료" },
];

export function supplierStatusLabel(status: string): string {
  return supplierStatuses.find((s) => s.value === status)?.label || status;
}

// approvalStatuses is the part of a 업무 객체's status the 결재 흐름 owns, in the
// words the approval screen already uses for the decisions that write them.
// These are the same four the API refuses to take from a request body —
// workflowOwnedStatus in objects.go — with the 초안 they start from, because a
// status the workflow awards is the one thing a caller may not name for itself.
export const approvalStatuses: { value: string; label: string }[] = [
  { value: "draft", label: "초안" },
  { value: "pending_approval", label: "승인 대기" },
  { value: "approved", label: "승인" },
  { value: "returned", label: "보완 요청" },
  { value: "rejected", label: "반려" },
];

// submittableStatuses are the states a 업무 객체 can be sent for approval from.
//
// 보완 요청 is the decision an approver makes to hand a request back to be
// fixed and sent again; it is the whole difference between it and 반려. But the
// list only ever offered 승인 요청 on an object reading "draft", so the request
// came back and stopped there: the button was gone, the row's checkbox was
// disabled, the 상태 filter had no 보완 요청 option to find it by, and the badge
// showed the raw English word in the neutral tone every unknown status gets. The
// author's only way forward was to type the whole thing in again as a new
// request — which is 반려 with nobody told, arrived at through the button that
// exists so that would not happen.
export const submittableStatuses = ["draft", "returned"];

export function canSubmitForApproval(status?: string): boolean {
  return submittableStatuses.includes(status || "");
}

// objectStatusLabels covers the statuses the application itself writes into a
// 업무 객체 — the approval decisions above, the words sourcing awards, and the
// lifecycle states the object list filters on. A status not on it keeps showing
// as it is stored, which is what every status did before this map existed.
const objectStatusLabels: Record<string, string> = {
  ...Object.fromEntries(approvalStatuses.map((s) => [s.value, s.label])),
  open: "공개",
  preferred_negotiation: "우선협상",
  selected: "선정",
  confirmed: "확정",
  sent: "발송",
  executed: "체결",
  active: "진행 중",
  completed: "완료",
  accepted: "인수",
  closed: "종결",
  resolved: "해결",
  ended: "종료",
  terminated: "해지",
};

export function objectStatusLabel(status: string): string {
  return objectStatusLabels[status] || status;
}

// objectStatusFilters is what the 업무 객체 list offers to filter by: the
// approval lifecycle plus the two states an object runs in afterwards. It is
// listed here rather than in the page so that a status the workflow gains
// cannot go missing from the only screen that would show it.
export const objectStatusFilters: { value: string; label: string }[] = [
  { value: "draft", label: "초안" },
  { value: "pending_approval", label: "승인 대기" },
  { value: "approved", label: "승인" },
  { value: "active", label: "진행 중" },
  { value: "completed", label: "완료" },
  { value: "returned", label: "보완 요청" },
  { value: "rejected", label: "반려" },
];

// workflowObjectTypes is the 업무 유형 an approval rule can be written for, in
// the order the API holds them in workflowObjectTypes(). It is the type name
// the routing matches on, not a caption, so a rule filed under anything else
// never fires and the submission it was meant to hold is approved on the spot.
//
// The form wrote its own four-item list, and it was wrong in both directions.
// 공급업체 saved "supplier", a type no submit path in the application uses, so
// the rule sat in the list as 활성 and matched nothing. And the eight types it
// left out — 납품, 검수, 품질, 이슈, RFQ, RFP, Invoice, 지급 — all carry a 승인
// 요청 button with no way to put an approval in front of it: an invoice was
// stamped 승인 the moment it was sent, by nobody.
export const workflowObjectTypes: { value: string; label: string }[] = [
  { value: "contract", label: "계약" },
  { value: "purchase_request", label: "구매요청" },
  { value: "rfq", label: "RFQ" },
  { value: "rfp", label: "RFP · 입찰" },
  { value: "purchase_order", label: "발주" },
  { value: "delivery", label: "납품" },
  { value: "inspection", label: "검수" },
  { value: "quality", label: "품질 · CAPA" },
  { value: "issue", label: "공급업체 이슈" },
  { value: "invoice", label: "Invoice" },
  { value: "payment", label: "지급" },
  // Not a record anyone files: changing a supplier's bank account opens one
  // and submits it in the same step. The rule installed with the schema is
  // written for it, and the Workflow list is the only screen that shows it.
  { value: "supplier_bank_change", label: "공급업체 계좌정보 변경" },
];

// permissionCodes is every permission the API actually checks, in the order it
// holds them in permissionCodes(). A role's permissions are only read on the
// wanted side of that check, so a word that is not one of these — or a wildcard
// that covers none of them — is not a narrower permission but no permission:
// the role lists it and opens nothing.
//
// The 권한 box had no list at all behind it, which is how the catalogue the
// product ships with came to hold "risk.security.*", "risk.contract.*" and
// "contract.review", none of which any door asks for.
export const permissionCodes: string[] = [
  "*",
  "*.read",
  "ai.use",
  "analytics.read",
  "audit.read",
  "contract.amount.read",
  "contract.create",
  "contract.read",
  "contract.update",
  "dashboard.read",
  "delivery.amount.read",
  "delivery.create",
  "delivery.read",
  "delivery.update",
  "document.create",
  "document.read",
  "document.update",
  "evaluation.create",
  "evaluation.read",
  "inspection.amount.read",
  "inspection.create",
  "inspection.read",
  "inspection.update",
  "invoice.amount.read",
  "invoice.create",
  "invoice.read",
  "invoice.update",
  "issue.amount.read",
  "issue.create",
  "issue.read",
  "issue.update",
  "payment.amount.read",
  "payment.create",
  "payment.read",
  "payment.update",
  "portal.*",
  "purchase_order.amount.read",
  "purchase_order.create",
  "purchase_order.read",
  "purchase_order.update",
  "purchase_request.amount.read",
  "purchase_request.create",
  "purchase_request.read",
  "purchase_request.update",
  "quality.amount.read",
  "quality.create",
  "quality.read",
  "quality.update",
  "rfp.amount.read",
  "rfp.create",
  "rfp.read",
  "rfp.update",
  "rfq.amount.read",
  "rfq.create",
  "rfq.read",
  "rfq.update",
  "risk.create",
  "risk.read",
  "spend.create",
  "spend.read",
  "supplier.bank_account.read",
  "supplier.create",
  "supplier.financial.read",
  "supplier.read",
  "supplier.tax.read",
  "supplier.update",
  "workflow.approve",
  "workflow.create",
  "workflow.read",
  "workflow.update",
];

export function workflowObjectTypeLabel(objectType: string): string {
  return (
    workflowObjectTypes.find((t) => t.value === objectType)?.label || objectType
  );
}

// sourcingParticipantLabels is the vocabulary a bidder's standing in an RFQ/RFP
// is written in — the same list the API holds in sourcingParticipantStatuses,
// plus the 마감 the portal reports once the due date has passed.
//
// Neither screen showing a standing had a word for it. The buyer's 참여 공급업체
// list and the supplier's own portal card both printed the stored value, so the
// company that had not been chosen read "not_selected" in the blue tone every
// unrecognised status gets — the same tone, and as much information, as the one
// that had won.
const sourcingParticipantLabels: Record<string, string> = {
  invited: "초대",
  draft: "작성 중",
  submitted: "제출 완료",
  declined: "참여 거절",
  preferred: "우선협상",
  selected: "선정",
  not_selected: "미선정",
  closed: "마감",
};

export function sourcingParticipantLabel(status: string): string {
  return sourcingParticipantLabels[status] || status;
}

// sourcingStandingIsTheCommittees reports whether a standing was written by the
// award rather than by the bidder. When it was, it is what the card says: the
// bidder's own submission status is no longer the news.
export function sourcingStandingIsTheCommittees(status?: string): boolean {
  return (
    status === "preferred" || status === "selected" || status === "not_selected"
  );
}

// sourcingBiddingClosed reports whether that answer is final, which is when the
// portal stops offering the response form — the same two standings the API
// refuses a save from. 우선협상 is not one of them: revising the quote is what
// that selection is for.
export function sourcingBiddingClosed(status?: string): boolean {
  return status === "selected" || status === "not_selected";
}

// invitationStatusLabels is what a Self Registration 초대 is at the moment the
// list is read — the same four the API computes in invitationStanding. Only
// 유효 is a live link: the other three are the ways one stops working, and the
// difference between them is the whole reason the list exists.
const invitationStatusLabels: Record<string, string> = {
  pending: "유효",
  accepted: "가입 완료",
  revoked: "회수됨",
  expired: "기간 만료",
};

export function invitationStatusLabel(status: string): string {
  return invitationStatusLabels[status] || status;
}

export function statusTone(status?: string): StatusTone {
  const value = (status || "").toLowerCase();
  if (
    [
      "active",
      "approved",
      "completed",
      "pass",
      "low",
      "s",
      "a",
      "preferred",
      "selected",
    ].includes(value)
  )
    return "success";
  if (
    [
      "high",
      "critical",
      "rejected",
      "suspended",
      "terminated",
      "failed",
      // The bidder was not chosen. It used to share the neutral blue with
      // 제출 완료, so losing an award looked like the bid was still in.
      "not_selected",
    ].includes(value)
  )
    return "danger";
  if (
    [
      "pending",
      "screening",
      "registration",
      "improvement",
      "medium",
      "conditional_pass",
      // A returned request is waiting on its author, not resting: it used to
      // fall through to the same blue "info" every unrecognised status gets,
      // which reads as nothing to do.
      "returned",
    ].includes(value)
  )
    return "warning";
  if (
    [
      "draft",
      "candidate",
      // An invitation that has been called back or has run out is not news
      // and not a problem: it is a link that no longer works.
      "revoked",
      "expired",
    ].includes(value)
  )
    return "neutral";
  return "info";
}
