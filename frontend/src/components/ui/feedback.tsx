import type { ReactNode } from "react";
import { cn } from "../../lib/utils";
import { Icon } from "../Icons";
export function Alert({
  children,
  kind = "info",
  className,
}: {
  children: ReactNode;
  kind?: "info" | "error" | "warning" | "success";
  className?: string;
}) {
  return (
    <div
      role={kind === "error" ? "alert" : "status"}
      className={cn("alert", `alert-${kind}`, className)}
    >
      <Icon
        name={
          kind === "error" || kind === "warning"
            ? "warning"
            : kind === "success"
              ? "check"
              : "shield"
        }
      />
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}
export function Progress({ value, label }: { value: number; label: string }) {
  return (
    <progress
      className="usage-progress"
      aria-label={label}
      max={100}
      value={Math.min(100, Math.max(0, value))}
    />
  );
}
