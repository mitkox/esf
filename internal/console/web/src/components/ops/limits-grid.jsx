import { formatBytes, formatDuration, formatNumber } from "@/lib/format";
import { asNumber } from "./data";
import { DefinitionList } from "./definition-list";

function bytesLimit(value) {
  return asNumber(value) === null ? undefined : formatBytes(value);
}

function durationLimit(value) {
  return asNumber(value) === null ? undefined : formatDuration(value);
}

function quantity(value, suffix) {
  return asNumber(value) === null ? undefined : `${formatNumber(value)} ${suffix}`;
}

// LimitsGrid is shared by Operations (effective worker limits) and Settings
// (configured limits) so both screens describe the same numbers identically.
export function LimitsGrid({ limits, className }) {
  const source = limits || {};
  const items = [
    { label: "Command output cap", value: bytesLimit(source.command_output_bytes), hint: "Captured per command before truncation" },
    { label: "Artifact cap", value: bytesLimit(source.artifact_bytes), hint: "Bounded evidence size per run" },
    { label: "Worker concurrency", value: asNumber(source.worker_concurrency) === null ? undefined : formatNumber(source.worker_concurrency) },
    { label: "Agent timeout", value: durationLimit(source.agent_timeout_ms) },
    { label: "Verification timeout", value: durationLimit(source.verification_timeout_ms) },
    { label: "Total run timeout", value: durationLimit(source.total_timeout_ms) },
    { label: "Sandbox idle timeout", value: durationLimit(source.sandbox_idle_timeout_ms) },
    { label: "CPU per sandbox", value: quantity(source.cpu_milli, "mCPU") },
    { label: "Memory per sandbox", value: quantity(source.memory_mib, "MiB") },
    { label: "Disk per sandbox", value: quantity(source.disk_mib, "MiB") },
  ];
  return <DefinitionList items={items} columns={3} className={className} />;
}
