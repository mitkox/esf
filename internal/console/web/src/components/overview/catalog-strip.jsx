import clsx from "clsx";
import { ArrowRight, Settings2 } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { formatNumber } from "@/lib/format";

const CATALOG_ITEMS = [
  { key: "harnesses", label: "Harnesses" },
  { key: "repositories", label: "Repositories" },
  { key: "verification_profiles", label: "Verification profiles" },
  { key: "budgets", label: "Budgets" },
  { key: "scopes", label: "Scopes" },
  { key: "egress_policies", label: "Egress policies" },
];

// CatalogStrip closes the page with the configured-resource inventory. It is
// deliberately read-only: the API never returns credential values.
export function CatalogStrip({ catalog, className }) {
  const counts = catalog || {};
  return (
    <Card className={clsx("min-w-0", className)}>
      <div className="flex flex-col gap-4 px-4 py-4 sm:px-5 lg:flex-row lg:items-center">
        <div className="flex min-w-0 items-start gap-2.5 lg:max-w-[290px]">
          <span className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-lg border border-border bg-surface-raised text-muted-foreground">
            <Settings2 aria-hidden="true" className="h-3.5 w-3.5" />
          </span>
          <div className="min-w-0">
            <p className="eyebrow">Configured resources</p>
            <p className="mt-1 text-[11px] leading-4 text-muted-foreground">
              Redacted view of <span className="mono">factory.toml</span>. Credential values are never returned by the API.
            </p>
          </div>
        </div>
        <div className="grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-3 lg:ml-auto lg:grid-cols-6">
          {CATALOG_ITEMS.map((item) => (
            <a
              key={item.key}
              href="#/settings"
              className="-mx-2 block min-w-0 rounded-lg px-2 py-1 transition-colors hover:bg-muted/60"
              title={`${item.label} configured in factory.toml`}
            >
              <span className="block truncate text-[9px] font-semibold uppercase leading-4 tracking-[0.1em] text-muted-foreground">
                {item.label}
              </span>
              <span className="tabular mt-0.5 block text-lg font-semibold leading-6 text-foreground">
                {counts[item.key] === null || counts[item.key] === undefined ? "—" : formatNumber(counts[item.key])}
              </span>
            </a>
          ))}
        </div>
        <Button href="#/settings" variant="ghost" size="xs" iconRight={<ArrowRight />} className="self-start lg:self-auto">
          Settings
        </Button>
      </div>
    </Card>
  );
}
