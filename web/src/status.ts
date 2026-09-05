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
  if (["draft", "candidate"].includes(value)) return "neutral";
  return "info";
}
