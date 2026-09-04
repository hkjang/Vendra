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
    ].includes(value)
  )
    return "warning";
  if (["draft", "candidate"].includes(value)) return "neutral";
  return "info";
}
