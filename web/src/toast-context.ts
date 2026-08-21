import { createContext, useContext } from "react";
import { APIError } from "./api";

export type ToastKind = "success" | "error" | "info";

/**
 * Report the outcome of an action to the user. Every screen has one, including
 * the supplier portal, so an action can never complete or fail unnoticed.
 */
export type Notify = (message: string, kind?: ToastKind) => void;

export const ToastContext = createContext<Notify | undefined>(undefined);

export function useNotify(): Notify {
  const notify = useContext(ToastContext);
  if (!notify) throw new Error("useNotify must be used inside ToastProvider");
  return notify;
}

/** Pull a human-readable message out of whatever a failed promise carried. */
export function errorMessage(reason: unknown, fallback: string): string {
  if (reason instanceof Error && reason.message) return reason.message;
  if (typeof reason === "string" && reason.trim()) return reason;
  return fallback;
}

/**
 * Whether the global safety net should announce this failure. An expired
 * session already sends the user back to the login screen, so reporting it
 * again would only add noise to a screen that is about to disappear.
 */
export function worthReporting(reason: unknown): boolean {
  return !(reason instanceof APIError && reason.status === 401);
}
