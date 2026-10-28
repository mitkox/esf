import { useCallback, useEffect, useState } from "react";
import { AlertTriangle, ArrowLeft, RefreshCw, SearchX } from "lucide-react";
import { api } from "@/lib/api";
import { useApi } from "@/lib/hooks";
import { useWorkspace } from "@/lib/workspace";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton, SkeletonText } from "@/components/ui/skeleton";
import { useToast } from "@/components/ui/toast";
import { RunDetailHeader } from "@/components/runs/run-detail-header";
import { LiveRunBanner, NoManifestNotice, RunNotices } from "@/components/runs/run-notices";
import { RunSummaryCards } from "@/components/runs/run-summary";
import { RunMetadataCard } from "@/components/runs/run-metadata";
import { TaskCard } from "@/components/runs/run-task";
import { ConditionsCard } from "@/components/runs/run-conditions";
import { VerificationCard } from "@/components/runs/run-verification";
import { SecurityCard } from "@/components/runs/run-security";
import { EvidenceBrowserCard } from "@/components/runs/run-artifacts";
import { PatchCard } from "@/components/runs/run-patch";
import { AuditCard } from "@/components/runs/run-audit";
import { OperatorCommandsCard } from "@/components/runs/run-commands";
import { PreviewsCard, RelatedChangeCard, RelatedRunsCard } from "@/components/runs/run-related";
import { ArtifactPreviewModal } from "@/components/runs/evidence-modal";
import { downloadText } from "@/components/runs/utils";

const CONTAINER = "mx-auto w-full max-w-[1600px] px-4 py-5 sm:px-6 lg:px-8";

function DetailSkeleton({ runID }) {
  return (
    <div className={CONTAINER}>
      <PageHeader
        backHref="#/runs"
        backLabel="All runs"
        eyebrow="Run evidence"
        title={
          <span className="block break-all font-mono text-[16px] font-semibold leading-snug sm:text-[19px]">{runID}</span>
        }
        description="Loading the durable run manifest and evidence…"
      />
      <div className="mb-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {[0, 1, 2, 3].map((item) => (
          <Card key={item}>
            <div className="px-3.5 pb-3 pt-3">
              <Skeleton className="h-2.5 w-24" />
              <Skeleton className="mt-2 h-6 w-28" />
              <Skeleton className="mt-2 h-2.5 w-36" />
            </div>
          </Card>
        ))}
      </div>
      <Card className="mb-4">
        <div className="px-4 py-3.5 sm:px-5">
          <SkeletonText lines={4} />
        </div>
      </Card>
      <div className="grid gap-4 xl:grid-cols-12">
        <div className="space-y-4 xl:col-span-8">
          {[0, 1].map((item) => (
            <Card key={item}>
              <div className="px-4 py-3.5 sm:px-5">
                <SkeletonText lines={6} />
              </div>
            </Card>
          ))}
        </div>
        <div className="space-y-4 xl:col-span-4">
          <Card>
            <div className="px-4 py-3.5 sm:px-5">
              <SkeletonText lines={5} />
            </div>
          </Card>
        </div>
      </div>
    </div>
  );
}

// RunDetailPage is the single-run evidence viewer. It fetches the run document,
// lazily pulls the unified patch text for the viewer and re-queries faster while
// the run is still live in Temporal.
export function RunDetailPage({ runID }) {
  const { autoRefresh, refreshNonce } = useWorkspace();
  const toast = useToast();
  const [preview, setPreview] = useState(null);
  const [patch, setPatch] = useState({ text: "", loading: false, error: null });
  const [patchNonce, setPatchNonce] = useState(0);
  const [downloading, setDownloading] = useState(false);
  const [livePoll, setLivePoll] = useState(false);

  const detail = useApi(`/runs/${encodeURIComponent(runID || "")}`, {
    enabled: Boolean(runID),
    pollMs: autoRefresh ? (livePoll ? 10000 : 60000) : 0,
    deps: [refreshNonce],
  });

  const data = detail.data;
  const liveRun = data && data.manifest === null && data.live ? data.live : null;
  const patchAvailable = Boolean(data?.patch?.available);

  useEffect(() => {
    setLivePoll(Boolean(data && data.manifest === null && data.live));
  }, [data]);

  // Fetch the patch text once the detail confirms a patch exists (or when the
  // operator asks for a retry). A missing patch is a normal state, not an
  // error, so only 404/read failures surface in the viewer.
  useEffect(() => {
    if (!runID || !patchAvailable) {
      setPatch({ text: "", loading: false, error: null });
      return undefined;
    }
    let cancelled = false;
    const controller = new AbortController();
    setPatch({ text: "", loading: true, error: null });
    api
      .text(`/runs/${encodeURIComponent(runID)}/patch`, {}, { signal: controller.signal })
      .then((text) => {
        if (!cancelled) setPatch({ text, loading: false, error: null });
      })
      .catch((error) => {
        if (!cancelled && error?.name !== "AbortError") setPatch({ text: "", loading: false, error });
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [runID, patchAvailable, refreshNonce, patchNonce]);

  const openArtifact = useCallback((path, label) => setPreview({ path, label: label || path }), []);
  const closePreview = useCallback(() => setPreview(null), []);

  const downloadPatch = useCallback(async () => {
    if (!runID) return;
    setDownloading(true);
    try {
      const text = patch.text || (await api.text(`/runs/${encodeURIComponent(runID)}/patch?download=true`));
      downloadText(`${runID}.patch`, text);
      toast({ tone: "success", title: "Patch downloaded", message: `${runID}.patch` });
    } catch (error) {
      toast({
        tone: "danger",
        title: "Patch download failed",
        message: error?.message || "The patch could not be read from the console API.",
      });
    } finally {
      setDownloading(false);
    }
  }, [runID, patch.text, toast]);

  if (!data && detail.loading) return <DetailSkeleton runID={runID} />;

  if (!data && detail.error) {
    const notFound = detail.error.status === 404;
    return (
      <div className={CONTAINER}>
        <PageHeader
          backHref="#/runs"
          backLabel="All runs"
          eyebrow="Run evidence"
          title={notFound ? "Run not found" : "Run evidence unavailable"}
          description={
            notFound
              ? `No durable evidence or live workflow matches this run ID.`
              : detail.error.message || "The run evidence could not be loaded."
          }
        />
        <Card>
          {notFound ? (
            <EmptyState
              icon={<SearchX aria-hidden="true" />}
              title="Nothing recorded for this run ID"
              description="The run may have been pruned from the evidence store, the ID may be mistyped, or the link may point at another factory environment."
              action={
                <Button as="a" href="#/runs" variant="primary" size="sm" icon={<ArrowLeft />}>
                  Back to runs
                </Button>
              }
            />
          ) : (
            <EmptyState
              icon={<AlertTriangle aria-hidden="true" />}
              title="Run evidence could not be loaded"
              description={detail.error.message || "The console server did not respond."}
              action={
                <div className="flex flex-wrap items-center justify-center gap-2">
                  <Button as="a" href="#/runs" variant="secondary" size="sm" icon={<ArrowLeft />}>
                    Back to runs
                  </Button>
                  <Button variant="primary" size="sm" icon={<RefreshCw />} onClick={detail.refresh}>
                    Try again
                  </Button>
                </div>
              }
            />
          )}
        </Card>
      </div>
    );
  }

  if (!data) return <DetailSkeleton runID={runID} />;

  return (
    <div className={CONTAINER}>
      <RunDetailHeader
        detail={data}
        live={Boolean(liveRun)}
        patchAvailable={patchAvailable}
        onDownloadPatch={downloadPatch}
        downloadBusy={downloading}
        updatedAt={detail.updatedAt}
      />

      {detail.error ? (
        <div
          role="status"
          className="mb-4 flex flex-wrap items-center gap-2 rounded-panel border border-warning/30 bg-warning/5 px-3 py-2 text-[12px] text-warning"
        >
          <AlertTriangle aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
          <span className="min-w-0 flex-1">The latest refresh failed: {detail.error.message}</span>
          <Button variant="ghost" size="xs" onClick={detail.refresh} className="text-warning hover:text-warning">
            Retry
          </Button>
        </div>
      ) : null}

      {liveRun ? <LiveRunBanner live={liveRun} run={data.run} /> : !data.manifest ? <NoManifestNotice /> : null}
      <RunNotices warnings={data.warnings} budgetExceeded={data.budget_exceeded} />
      <RunSummaryCards run={data.run} verification={data.verification} />
      <RunMetadataCard detail={data} />

      <div className="grid gap-4 xl:grid-cols-12">
        <div className="space-y-4 xl:col-span-8">
          <TaskCard task={data.task} live={Boolean(liveRun)} />
          <ConditionsCard conditions={data.conditions || []} live={Boolean(liveRun)} />
          <VerificationCard
            verification={data.verification}
            onPreviewArtifact={openArtifact}
            live={Boolean(liveRun)}
          />
          <SecurityCard run={data.run} security={data.security} />
          <EvidenceBrowserCard artifacts={data.artifacts || []} onPreview={openArtifact} />
          <PatchCard
            patchDetail={data.patch}
            text={patch.text}
            loading={patch.loading}
            error={patch.error}
            onRetry={() => setPatchNonce((value) => value + 1)}
            onDownload={downloadPatch}
            downloading={downloading}
          />
          <AuditCard entries={data.audit || []} />
        </div>
        <div className="space-y-4 xl:col-span-4">
          <OperatorCommandsCard detail={data} />
          <RelatedChangeCard change={data.change} run={data.run} />
          <PreviewsCard previews={data.previews || []} />
          <RelatedRunsCard runs={data.related_runs || []} />
        </div>
      </div>

      <ArtifactPreviewModal runID={runID} path={preview?.path} label={preview?.label} onClose={closePreview} />
    </div>
  );
}
