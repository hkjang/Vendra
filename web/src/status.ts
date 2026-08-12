export type StatusTone =
  | "neutral"
  | "success"
  | "warning"
  | "danger"
  | "info"
  | "purple";

export function statusTone(status?: string): StatusTone {
  const value = (status || "").toLowerCase();
  if (
    ["active", "approved", "completed", "pass", "low", "s", "a"].includes(
      value,
    )
  )
    return "success";
  if (
    ["high", "critical", "rejected", "suspended", "failed"].includes(value)
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
