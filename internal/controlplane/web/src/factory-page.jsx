import React, { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { PageHeading } from "@/components/ui/page-heading";

const key = "esf-factory-read-token";

async function factoryRequest(path, token, text = false) {
  const response = await fetch(`/api/v1/factory/${path}`, {
    headers: { Authorization: `Bearer ${token}`, Accept: text ? "text/plain" : "application/json" },
    cache: "no-store",
  });
  if (!response.ok) {
    const error = new Error(response.status === 401 ? "The factory read token was rejected." : `Factory evidence is unavailable (HTTP ${response.status}).`);
    error.status = response.status;
    throw error;
  }
  return text ? response.text() : response.json();
}

function date(value) {
  if (!value || value.startsWith("0001-")) return "Unknown";
  return new Date(value).toLocaleString();
}

export function FactoryPage({ available }) {
  const [token, setToken] = useState(() => sessionStorage.getItem(key) || "");
  const [draft, setDraft] = useState("");
  const [tab, setTab] = useState("runs");
  const [page, setPage] = useState({ runs: [], changes: [], next_cursor: "" });
  const [cursor, setCursor] = useState("");
  const [selected, setSelected] = useState(null);
  const [evidence, setEvidence] = useState([]);
  const [patch, setPatch] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!available || !token) return;
    let active = true;
    setLoading(true);
    factoryRequest(`${tab}?limit=20${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`, token)
      .then((result) => { if (active) { setPage(result); setError(""); } })
      .catch((failure) => {
        if (!active) return;
        setError(failure.message);
        if (failure.status === 401) { sessionStorage.removeItem(key); setToken(""); }
      })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [available, token, tab, cursor]);

  function unlock(event) {
    event.preventDefault();
    const value = draft.trim();
    if (!value) return;
    sessionStorage.setItem(key, value);
    setToken(value);
    setDraft("");
  }

  async function openRun(run) {
    setSelected(run);
    setPatch("");
    setEvidence([]);
    setError("");
    try {
      const result = await factoryRequest(`runs/${encodeURIComponent(run.run_id)}/evidence`, token);
      setEvidence(result.paths || []);
    } catch (failure) { setError(failure.message); }
  }

  async function showPatch() {
    try {
      const data = await factoryRequest(`runs/${encodeURIComponent(selected.run_id)}/patch`, token, true);
      setPatch(data);
    } catch (failure) { setError(failure.message); }
  }

  async function downloadPatch() {
    try {
      const data = await factoryRequest(`runs/${encodeURIComponent(selected.run_id)}/patch`, token, true);
      const url = URL.createObjectURL(new Blob([data], { type: "text/plain" }));
      const link = document.createElement("a");
      link.href = url;
      link.download = `${selected.run_id}.patch`;
      link.click();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (failure) { setError(failure.message); }
  }

  if (!available) return <div className="p-6">Factory evidence is not configured for this console.</div>;
  return <div className="mx-auto max-w-[1500px] space-y-5 p-4 sm:p-6 lg:p-8">
    <PageHeading title="Factory" description="Read-only runs, changes, evidence, and recovery status." />
    {!token ? <Card className="max-w-lg space-y-3 p-5">
      <h2 className="font-semibold">Unlock factory evidence</h2>
      <p className="text-sm text-muted-foreground">Enter the console's separate read token. It stays in this browser tab session.</p>
      <form onSubmit={unlock} className="flex gap-2"><input className="field-control" type="password" aria-label="Factory read token" autoComplete="off" value={draft} onChange={(event) => setDraft(event.target.value)} /><Button>Unlock</Button></form>
    </Card> : <>
      <div className="flex gap-2"><Button size="sm" variant={tab === "runs" ? "default" : "ghost"} onClick={() => { setTab("runs"); setCursor(""); setSelected(null); }}>Runs</Button><Button size="sm" variant={tab === "changes" ? "default" : "ghost"} onClick={() => { setTab("changes"); setCursor(""); setSelected(null); }}>Changes</Button><Button size="sm" variant="ghost" onClick={() => { sessionStorage.removeItem(key); setToken(""); setSelected(null); }}>Lock</Button></div>
      {error && <p role="alert" className="text-sm text-danger">{error}</p>}
      {loading && <p className="text-sm text-muted-foreground">Loading evidence…</p>}
      <div className="grid gap-4 xl:grid-cols-[minmax(280px,1fr)_minmax(0,2fr)]">
        <Card className="divide-y divide-border overflow-hidden">
          {(page[tab] || []).map((item) => <button key={item.run_id || item.change_id} className="block w-full p-4 text-left hover:bg-muted/50" onClick={() => tab === "runs" ? openRun(item) : setSelected(item)}>
            <strong className="block break-all text-sm">{item.run_id || item.change_id}</strong>
            <span className="block truncate text-xs text-muted-foreground">{item.repository || item.scope || "No repository"}</span>
            <span className="text-xs">{item.factory_result || item.status || item.state || "In progress"}</span>
          </button>)}
          {!loading && !(page[tab] || []).length && <p className="p-4 text-sm text-muted-foreground">No {tab} found.</p>}
          {page.next_cursor && <div className="p-3"><Button size="sm" variant="ghost" onClick={() => { setCursor(page.next_cursor); setSelected(null); }}>Next page</Button></div>}
        </Card>
        <Card className="min-w-0 space-y-5 p-5">
          {!selected ? <p className="text-sm text-muted-foreground">Select a {tab === "runs" ? "run" : "change"} to inspect its saved record.</p> : tab === "changes" ? <>
            <h2 className="break-all text-lg font-semibold">Change {selected.change_id}</h2>
            <p className="text-sm">Status: {selected.status || selected.state || "Unknown"}</p>
            <p className="text-xs text-muted-foreground">Updated {date(selected.updated_at)}</p>
            <p className="text-xs text-muted-foreground">Controlled decisions remain in the authenticated factory CLI.</p>
          </> : <>
            <div><h2 className="break-all text-lg font-semibold">Run {selected.run_id}</h2><p className="break-all text-sm text-muted-foreground">{selected.repository} · {selected.agent_harness} · {selected.sandbox_template}</p></div>
            <div className="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
              <p><strong>Result</strong><br />{selected.factory_result || "Pending"}</p><p><strong>Cleanup</strong><br />{selected.cleanup_result?.outcome || "See manifest"}{selected.cleanup_result?.verified && " · verified"}</p><p><strong>Started</strong><br />{date(selected.started_at)}</p>
              <p><strong>Completed</strong><br />{date(selected.completed_at)}</p><p><strong>Tokens</strong><br />{selected.tokens_in == null && selected.tokens_out == null ? "Unavailable" : `${selected.tokens_in ?? "?"} in · ${selected.tokens_out ?? "?"} out`}</p><p><strong>Cost</strong><br />{selected.inference_cost == null ? "Unavailable" : `${selected.inference_cost}`}</p>
            </div>
            <section><h3 className="mb-2 text-sm font-semibold">Timeline and gates</h3><ol className="space-y-2 border-l border-border pl-4 text-sm">{(selected.conditions || []).map((condition, index) => <li key={index}><strong>{condition.type || condition.name || `Gate ${index + 1}`}</strong> · {condition.status || "Unknown"}{condition.reason && <span className="block text-xs text-muted-foreground">{condition.reason}</span>}</li>)}</ol></section>
            <section className="space-y-1 text-sm"><h3 className="font-semibold">Approval and quality</h3><p>Review: {selected.review_configured ? selected.review_outcome || "Pending" : "Not configured"}</p><p>Quality: {selected.quality ? selected.quality.decision ? JSON.stringify(selected.quality.decision) : "No decision recorded" : "No QMS summary in this manifest"}</p>{selected.quality?.missing?.length > 0 && <p>Missing controls: {selected.quality.missing.join(", ")}</p>}<p className="text-xs text-muted-foreground">The QMS authority controls decisions; this view only shows the saved run summary.</p></section>
            <section className="space-y-1 text-sm"><h3 className="font-semibold">Evidence</h3>{evidence.length ? <ul className="max-h-36 overflow-auto font-mono text-xs">{evidence.map((path) => <li key={path}>{path}</li>)}</ul> : <p className="text-muted-foreground">No evidence paths reported.</p>}</section>
            {selected.patch && <section className="space-y-2"><h3 className="text-sm font-semibold">Patch</h3><div className="flex gap-2"><Button size="sm" variant="ghost" onClick={showPatch}>Preview</Button><Button size="sm" variant="ghost" onClick={downloadPatch}>Download</Button></div>{patch && <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted p-3 text-xs">{patch}</pre>}</section>}
            {(selected.cleanup_result?.outcome === "ERROR" || selected.cleanup_result?.remaining_sandboxes > 0 || selected.error) && <p className="rounded-md border border-danger/35 p-3 text-sm">{selected.error || selected.cleanup_result?.error || "Cleanup needs reconciliation."} Check the factory CLI and reconcile the run before resubmitting work.</p>}
          </>}
        </Card>
      </div>
    </>}
  </div>;
}
