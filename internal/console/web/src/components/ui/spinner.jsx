import { LoaderCircle } from "lucide-react";
import clsx from "clsx";

const SIZES = {
  xs: "h-3 w-3",
  sm: "h-3.5 w-3.5",
  md: "h-4 w-4",
  lg: "h-5 w-5",
};

// Spinner is presentational: loading announcements belong to the surrounding
// region (skeleton with role=status, button aria-busy, etc.).
export function Spinner({ size = "sm", className, label }) {
  const icon = (
    <LoaderCircle
      aria-hidden="true"
      strokeWidth={2.25}
      className={clsx("animate-spin", SIZES[size] || SIZES.sm, className)}
    />
  );
  if (!label) return icon;
  return (
    <span role="status" aria-label={label} className="inline-flex items-center justify-center">
      {icon}
    </span>
  );
}
