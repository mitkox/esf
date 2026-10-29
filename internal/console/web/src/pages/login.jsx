import { useRef, useState } from "react";
import clsx from "clsx";
import {
  ArrowRight,
  Eye,
  EyeOff,
  KeyRound,
  Lock,
  ScrollText,
  ShieldCheck,
  TriangleAlert,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { StatusDot } from "@/components/ui/badge";

const FEATURES = [
  {
    Icon: ShieldCheck,
    title: "Read-only by construction",
    description: "No mutation path exists from this surface. Decisions stay in the authenticated CLI.",
  },
  {
    Icon: ScrollText,
    title: "Evidence, not estimates",
    description: "Every metric derives from the durable run and change artifact store.",
  },
  {
    Icon: KeyRound,
    title: "Tab-scoped credentials",
    description: "The read token is held in sessionStorage for this browser tab only.",
  },
];

function BrandLockup({ compact = false }) {
  return (
    <div className="flex items-center gap-3">
      <div
        className={clsx(
          "grid shrink-0 place-items-center rounded-xl border border-primary/25 bg-primary/10 text-primary",
          compact ? "h-9 w-9" : "h-11 w-11",
        )}
      >
        <span className={clsx("font-extrabold tracking-tight", compact ? "text-[10px]" : "text-[12px]")}>ESF</span>
      </div>
      <div className="min-w-0">
        <p className={clsx("truncate font-semibold tracking-tight", compact ? "text-[13px]" : "text-sm")}>
          Engineering Software Factory
        </p>
        <p className="truncate text-[11px] leading-4 text-muted-foreground">Enterprise operator console</p>
      </div>
    </div>
  );
}

export function LoginScreen({ error, onLogin }) {
  const [token, setToken] = useState("");
  const [visible, setVisible] = useState(false);
  const [busy, setBusy] = useState(false);
  const inputRef = useRef(null);

  async function submit(event) {
    event.preventDefault();
    const value = token.trim();
    if (!value || busy) return;
    setBusy(true);
    try {
      await onLogin?.(value);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="relative flex min-h-screen flex-col bg-background text-foreground">
      <div aria-hidden="true" className="glow-top pointer-events-none absolute inset-x-0 top-0 h-[420px]" />

      <div className="relative grid flex-1 lg:grid-cols-[minmax(0,1fr)_minmax(0,540px)]">
        {/* Narrative panel: presence on desktop, omitted on phones where the
            token form must stay above the fold. */}
        <section className="hidden flex-col justify-between border-r border-border bg-surface/30 px-10 py-10 lg:flex xl:px-14 xl:py-12">
          <BrandLockup />
          <div className="max-w-lg">
            <p className="eyebrow">Mission control</p>
            <h1 className="mt-3 text-[32px] font-semibold leading-[1.14] tracking-[-0.03em] text-foreground xl:text-[36px]">
              Every run, change and decision in one evidence surface.
            </h1>
            <p className="mt-4 max-w-md text-sm leading-relaxed text-muted-foreground">
              Dense, keyboard-first control for platform admins, security reviewers and auditors. Read-only
              by design — nothing on this surface invents a number.
            </p>
            <ul className="mt-9 space-y-4">
              {FEATURES.map((feature) => (
                <li key={feature.title} className="flex items-start gap-3">
                  <span className="mt-0.5 grid h-8 w-8 shrink-0 place-items-center rounded-lg border border-border bg-surface-raised text-muted-foreground">
                    <feature.Icon aria-hidden="true" className="h-3.5 w-3.5" />
                  </span>
                  <div>
                    <p className="text-[13px] font-medium leading-5 text-foreground">{feature.title}</p>
                    <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">{feature.description}</p>
                  </div>
                </li>
              ))}
            </ul>
          </div>
          <div className="flex items-center gap-2 text-[11px] leading-4 text-muted-foreground">
            <StatusDot tone="success" pulse />
            Loopback operator tool · read-only access · no telemetry
          </div>
        </section>

        {/* Authentication panel */}
        <section className="flex flex-1 flex-col px-4 py-8 sm:px-6 lg:px-10 lg:py-10">
          <div className="mb-8 lg:hidden">
            <BrandLockup compact />
          </div>
          <div className="mx-auto my-auto w-full max-w-[400px]">
            <div className="mb-6">
              <p className="eyebrow">Operator console</p>
              <h2 className="mt-2 text-2xl font-semibold tracking-[-0.02em] text-foreground">Unlock the factory console</h2>
              <p className="mt-2 text-[13px] leading-relaxed text-muted-foreground">
                Enter the console read token configured on the factory host. It is sent as a bearer
                credential and never placed in a URL.
              </p>
            </div>

            <form onSubmit={submit} className="rounded-panel border border-border bg-surface p-5 shadow-panel sm:p-6">
              <label htmlFor="console-token" className="block text-[11px] font-semibold uppercase tracking-[0.1em] text-muted-foreground">
                Access token
              </label>
              <div className="relative mt-2">
                <KeyRound
                  aria-hidden="true"
                  className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
                />
                <Input
                  ref={inputRef}
                  id="console-token"
                  type={visible ? "text" : "password"}
                  value={token}
                  autoFocus
                  autoComplete="off"
                  autoCapitalize="off"
                  spellCheck={false}
                  placeholder="32+ character read token"
                  aria-describedby="token-help"
                  invalid={Boolean(error)}
                  onChange={(event) => setToken(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Escape") {
                      event.preventDefault();
                      setToken("");
                    }
                  }}
                  className="h-10 pl-8 pr-10 text-[13px]"
                />
                <button
                  type="button"
                  onClick={() => setVisible((current) => !current)}
                  aria-label={visible ? "Hide token" : "Show token"}
                  aria-pressed={visible}
                  className="absolute right-1.5 top-1/2 grid h-7 w-7 -translate-y-1/2 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                >
                  {visible ? <EyeOff aria-hidden="true" className="h-3.5 w-3.5" /> : <Eye aria-hidden="true" className="h-3.5 w-3.5" />}
                </button>
              </div>
              <p id="token-help" className="mt-2 text-[11px] leading-4 text-muted-foreground">
                Set with <span className="mono text-[10px]">--read-token-file</span> when starting{" "}
                <span className="mono text-[10px]">factory console</span>.
              </p>

              {error ? (
                <div
                  role="alert"
                  className="mt-3 flex items-start gap-2 rounded-lg border border-danger/30 bg-danger/5 px-2.5 py-2 text-[11px] leading-5 text-danger"
                >
                  <TriangleAlert aria-hidden="true" className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                  <span>{error}</span>
                </div>
              ) : null}

              <Button
                type="submit"
                variant="primary"
                size="lg"
                loading={busy}
                disabled={!token.trim()}
                iconRight={busy ? null : <ArrowRight className="h-3.5 w-3.5" />}
                className="mt-4 h-10 w-full text-[13px]"
              >
                {busy ? "Verifying token…" : "Enter console"}
              </Button>

              <div className="mt-3 flex items-center justify-center gap-1.5 text-[10px] leading-4 text-muted-foreground">
                <span className="kbd">Enter</span>
                connects
                <span className="px-0.5 text-muted-foreground/40">·</span>
                <span className="kbd">Esc</span>
                clears
              </div>
            </form>

            <div className="mt-4 flex items-start gap-2 rounded-lg border border-border bg-surface/50 px-3 py-2.5 text-[11px] leading-relaxed text-muted-foreground">
              <Lock aria-hidden="true" className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>
                The console is read-only. Review decisions and run cancellation remain in the authenticated{" "}
                <span className="mono text-[10px] text-foreground/80">factory</span> CLI.
              </span>
            </div>

            <p className="mt-5 text-center text-[10px] leading-4 text-muted-foreground/70">
              ESF Console · loopback operator surface · credentials stay in this tab
            </p>
          </div>
        </section>
      </div>
    </div>
  );
}
