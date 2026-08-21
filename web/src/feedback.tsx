import { ReactNode, useCallback, useEffect, useMemo, useState } from "react";
import { Toast } from "./components";
import {
  errorMessage,
  Notify,
  ToastContext,
  ToastKind,
  worthReporting,
} from "./toast-context";

type QueuedToast = { id: number; message: string; kind: ToastKind };

let nextToastId = 0;

// Errors need longer than a confirmation: the user has to read them and often
// decide what to do next.
const dismissAfter: Record<ToastKind, number> = {
  success: 4500,
  info: 6000,
  error: 12000,
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<QueuedToast[]>([]);
  const dismiss = useCallback(
    (id: number) => setToasts((current) => current.filter((t) => t.id !== id)),
    [],
  );
  const notify = useCallback<Notify>((message, kind = "success") => {
    if (!message) return;
    setToasts((current) => {
      // One failure often produces several identical rejections. Showing the
      // same sentence three times tells the user nothing extra, so refresh the
      // existing toast instead of stacking duplicates.
      const duplicate = current.find(
        (t) => t.message === message && t.kind === kind,
      );
      if (duplicate) {
        return current.map((t) =>
          t === duplicate ? { ...t, id: ++nextToastId } : t,
        );
      }
      // Keep the stack short so a burst of failures cannot bury the screen.
      return [...current.slice(-2), { id: ++nextToastId, message, kind }];
    });
  }, []);

  // A safety net, not a substitute for handling errors where they happen: many
  // actions await an API call without a catch, and without this the request
  // simply fails in silence while the user waits for something to change.
  useEffect(() => {
    const onRejection = (event: PromiseRejectionEvent) => {
      if (!worthReporting(event.reason)) return;
      notify(errorMessage(event.reason, "요청을 처리하지 못했습니다"), "error");
    };
    window.addEventListener("unhandledrejection", onRejection);
    return () => window.removeEventListener("unhandledrejection", onRejection);
  }, [notify]);

  const value = useMemo(() => notify, [notify]);
  return (
    <ToastContext.Provider value={value}>
      {children}
      {toasts.length > 0 && (
        <div className="toast-stack">
          {toasts.map((toast) => (
            <Toast
              key={toast.id}
              type={toast.kind}
              message={toast.message}
              duration={dismissAfter[toast.kind]}
              onClose={() => dismiss(toast.id)}
            />
          ))}
        </div>
      )}
    </ToastContext.Provider>
  );
}
