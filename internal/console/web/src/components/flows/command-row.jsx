import clsx from "clsx";
import { CircleCheck, CircleX, SquareTerminal } from "lucide-react";
import { CopyButton } from "@/components/ui/copy-button";
import { TONE_TEXT } from "@/components/ui/tones";

const ICONS = {
  success: CircleCheck,
  danger: CircleX,
  info: SquareTerminal,
  warning: SquareTerminal,
};

// CommandRow shows one exact CLI command with a copy affordance. The console
// never executes these commands; they are the audited operator path.
export function CommandRow({ label, command, tone = "info", hint, className }) {
  const Icon = ICONS[tone] || ICONS.info;
  const available = Boolean(command);
  return (
    <div
      className={clsx(
        "flex items-start gap-2 rounded-lg border border-border bg-surface-raised/50 px-2.5 py-2",
        className,
      )}
    >
      <Icon aria-hidden="true" className={clsx("mt-0.5 h-3.5 w-3.5 shrink-0", TONE_TEXT[tone] || TONE_TEXT.muted)} />
      <div className="min-w-0 flex-1">
        <p className="text-[9.5px] font-semibold uppercase leading-4 tracking-[0.12em] text-muted-foreground">{label}</p>
        {available ? (
          <code className="mono mt-0.5 block break-all text-[11.5px] leading-5 text-foreground" title={command}>
            {command}
          </code>
        ) : (
          <p className="mt-0.5 text-[11.5px] text-muted-foreground">Command not reported</p>
        )}
        {hint ? <p className="mt-1 text-[10.5px] leading-4 text-muted-foreground">{hint}</p> : null}
      </div>
      {available ? <CopyButton value={command} label={label} size="sm" className="-mr-1 -mt-0.5 shrink-0" /> : null}
    </div>
  );
}
