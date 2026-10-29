import clsx from "clsx";

const SIDES = {
  top: "bottom-full left-1/2 mb-1.5 -translate-x-1/2",
  bottom: "top-full left-1/2 mt-1.5 -translate-x-1/2",
  left: "right-full top-1/2 mr-1.5 -translate-y-1/2",
  right: "left-full top-1/2 ml-1.5 -translate-y-1/2",
};

// Hint adds a CSS-only tooltip around its trigger. The tip is decorative
// (aria-hidden): icon-only controls must still carry their own aria-label.
export function Hint({ label, children, side = "top", className }) {
  if (!label) return children;
  return (
    <span className={clsx("hint", className)}>
      {children}
      <span role="tooltip" aria-hidden="true" className={clsx("hint-tip", SIDES[side] || SIDES.top)}>
        {label}
      </span>
    </span>
  );
}
