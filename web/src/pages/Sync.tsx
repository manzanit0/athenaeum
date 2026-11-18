import { type FormEvent, useEffect, useState } from "react";
import {
  api,
  type Job,
  type SyncDirection,
  type SyncInfo,
  type SyncProgress,
  type SyncReport,
  type SyncResult,
} from "../api";
import { bytes, count, dateTime } from "../format";
import { useLoad } from "../hooks";
import { useNotices } from "../notices";
import { Side, unchanged } from "../SyncChanges";
import { Button, Field, PageHeader, Problem, Section } from "../ui";

const directions: { direction: SyncDirection; label: string; about: string }[] = [
  {
    direction: "both",
    label: "Sync both ways",
    about: "Uploads what MEGA is missing, downloads what this library is missing, and removes from MEGA the books deleted here.",
  },
  { direction: "push", label: "Upload to MEGA", about: "Makes the MEGA folder catch up with this library." },
  { direction: "pull", label: "Download from MEGA", about: "Makes this library catch up with the MEGA folder." },
];

const pollInterval = 600;

const phases: Record<SyncProgress["phase"], string> = {
  preparing: "Comparing the library with MEGA",
  uploading: "Uploading",
  downloading: "Downloading",
  finishing: "Finishing",
};

/** Says how much a direction of a sync transfers, if anything. */
function transfer(result: SyncResult | undefined, planned: string, done: string): string | undefined {
  if (!result || result.files === 0) {
    return undefined;
  }
  return `${bytes(result.bytes)} ${result.dry_run ? planned : done}`;
}

/** How far the running sync has come: a bar, counts and the current file. */
function Progress({ job }: { job: Job }) {
  const progress = job.progress;
  const transferring = progress && progress.total > 0;
  const fraction = !transferring
    ? 0
    : progress.bytes_total > 0
      ? progress.bytes_done / progress.bytes_total
      : progress.done / progress.total;
  const percent = Math.round(Math.min(Math.max(fraction, 0), 1) * 100);
  const label = phases[progress?.phase ?? "preparing"];
  return (
    <div aria-label="Sync progress" role="group" className="max-w-[62ch] border-l-4 border-ink bg-sheet px-5 py-4">
      <p className="font-medium">{job.title}</p>
      <div
        role="progressbar"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={transferring ? percent : undefined}
        className="mt-3 h-2 overflow-hidden rounded-full bg-rule"
      >
        <div
          className={`h-full bg-ink transition-[width] duration-300 ${transferring ? "" : "animate-pulse"}`}
          style={{ width: transferring ? `${percent}%` : "100%" }}
        />
      </div>
      <p className="mt-2 tabular-nums" aria-live="polite">
        {transferring ? (
          <>
            {label} {Math.min(progress.done + 1, progress.total)} of {progress.total}
            {progress.bytes_total > 0 && (
              <span className="text-soft">
                , {bytes(progress.bytes_done)} of {bytes(progress.bytes_total)}
              </span>
            )}
          </>
        ) : (
          `${label}…`
        )}
      </p>
      {transferring && progress.current && <p className="mt-0.5 truncate text-[0.9rem] text-soft">{progress.current}</p>}
    </div>
  );
}

/** Sums up a sync that ran before syncs listed their changes by book. */
function outline(report: SyncReport): string {
  const parts = [
    ...(report.removed.length > 0 ? [`${count(report.removed.length, "file")} removed from MEGA`] : []),
    ...(report.push ? [`${count(report.push.files, "file")} ${report.dry_run ? "to upload" : "uploaded"}`] : []),
    ...(report.pull ? [`${count(report.pull.files, "file")} ${report.dry_run ? "to download" : "downloaded"}`] : []),
  ];
  return parts.join(", ");
}

/**
 * The outcome of the most recent sync, or what its preview plans: the
 * books added, updated and removed on MEGA and in the library. A preview
 * ends with the button that makes its changes.
 */
function LastRun({ job, onApply }: { job: Job; onApply?: (report: SyncReport) => void }) {
  const report = job.result as SyncReport | undefined;
  const when = dateTime(job.finished_at ?? job.created_at);
  const planned = Boolean(report?.dry_run);
  const described = Boolean(report?.mega || report?.library);
  const settled = described && unchanged(report?.mega) && unchanged(report?.library);
  return (
    <div
      aria-label={planned ? "Sync preview" : "Last sync"}
      role="group"
      className="max-w-[62ch] border-l-4 border-ink bg-sheet px-5 py-4"
    >
      <p className="font-medium">
        {job.title}
        <span className="font-normal text-soft">, {job.status === "running" ? "running now" : when}</span>
      </p>
      {job.status === "failed" && <p className="mt-1 text-ribbon-deep">Failed: {job.error}</p>}
      {report && !described && <p className="mt-1 text-soft">{outline(report)}</p>}
      {described && planned && !settled && (
        <p className="mt-1 text-soft">Nothing has changed yet. This is what the sync would do.</p>
      )}
      {settled && (
        <p className="mt-1">
          {planned ? "Nothing to do: the library and MEGA already match." : "Nothing changed: the library and MEGA already matched."}
        </p>
      )}
      {report?.mega && !settled && (
        <Side
          title="On MEGA"
          summary={report.mega}
          planned={planned}
          transfer={transfer(report.push, "to upload", "uploaded")}
          removalNote="What is removed from MEGA goes to its Rubbish Bin."
        />
      )}
      {report?.library && !settled && (
        <Side
          title="In this library"
          summary={report.library}
          planned={planned}
          transfer={transfer(report.pull, "to download", "downloaded")}
          removalNote="What is removed from this library is deleted for good."
        />
      )}
      {report && described && planned && !settled && onApply && (
        <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-rule pt-4">
          <Button variant="primary" onClick={() => onApply(report)}>
            Make these changes
          </Button>
          <p className="min-w-[14rem] flex-1 text-[0.9rem] text-soft">
            The library and MEGA are compared again first, so anything changed since this preview is included.
          </p>
        </div>
      )}
    </div>
  );
}

/**
 * The MEGA account the library syncs with: a form to sign in, which asks
 * for the two-factor code only when the account has one, or the account
 * in use with a way to sign out.
 */
function Account({ info, onChanged }: { info: SyncInfo; onChanged: () => void }) {
  const { notify, attempt } = useNotices();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [asksCode, setAsksCode] = useState(false);
  const [problem, setProblem] = useState("");
  const [busy, setBusy] = useState(false);

  const signIn = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setProblem("");
    await attempt(async () => {
      const result = await api.signIn(email, password, code);
      if (result.signed_in) {
        setPassword("");
        setCode("");
        setAsksCode(false);
        notify("Signed in to MEGA");
        onChanged();
        return;
      }
      setAsksCode(asksCode || result.code_required);
      setProblem(result.error);
    });
    setBusy(false);
  };
  const signOut = () =>
    attempt(async () => {
      const result = await api.signOut();
      if (result.confirmed) {
        notify("Signed out of MEGA");
      } else {
        notify("Signed out here, but MEGA could not be reached to end the session. End it from your MEGA account.", "error");
      }
      onChanged();
    });

  if (info.signed_in) {
    return (
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
        <p>Signed in to MEGA as {info.account}.</p>
        <Button onClick={signOut}>Sign out</Button>
      </div>
    );
  }
  return (
    <form onSubmit={signIn} className="max-w-xl">
      {info.error && <Problem>{info.error}</Problem>}
      <div className="mt-1 grid gap-x-4 gap-y-4 sm:grid-cols-2">
        <Field
          label="MEGA email"
          type="email"
          autoComplete="username"
          required
          value={email}
          onChange={(event) => setEmail(event.target.value)}
        />
        <Field
          label="MEGA password"
          type="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(event) => setPassword(event.target.value)}
        />
        {asksCode && (
          <Field
            label="Two-factor code"
            inputMode="numeric"
            autoComplete="one-time-code"
            hint="The six digits your authenticator app shows now."
            value={code}
            onChange={(event) => setCode(event.target.value.replace(/\D/g, "").slice(0, 6))}
          />
        )}
      </div>
      {problem && (
        <p role="alert" className="mt-3 text-ribbon-deep">
          {problem}
        </p>
      )}
      <div className="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2">
        <Button type="submit" variant="primary" disabled={busy}>
          {busy ? "Signing in…" : "Sign in"}
        </Button>
      </div>
      <p className="mt-3 max-w-[62ch] text-[0.9rem] text-soft">
        The password goes to MEGA once, to sign in, and is not kept. What is kept, encrypted in the database of this
        library, is the session MEGA answers with, until you sign out.
      </p>
    </form>
  );
}

/** Keeps the library in step with a folder on MEGA. */
export function SyncPage() {
  const { notify, track, attempt } = useNotices();
  const info = useLoad(api.sync, []);
  const [folder, setFolder] = useState<string>();
  const [mirror, setMirror] = useState(false);
  const [preview, setPreview] = useState(false);
  const [active, setActive] = useState<Job>();
  const data = info.data;
  const lastRun = data?.last_run;
  const watched = active?.id ?? (lastRun?.status === "running" ? lastRun.id : undefined);
  const running = watched !== undefined;
  const ready = Boolean(data?.signed_in && data.folder) && !running;

  useEffect(() => {
    if (watched === undefined) {
      return;
    }
    let timer = 0;
    let stopped = false;
    const poll = async () => {
      const job = await api.job(watched).catch(() => undefined);
      if (stopped) {
        return;
      }
      if (job && job.status !== "running") {
        setActive(undefined);
        info.reload();
        return;
      }
      if (job) {
        setActive(job);
      }
      timer = window.setTimeout(poll, pollInterval);
    };
    poll();
    return () => {
      stopped = true;
      window.clearTimeout(timer);
    };
  }, [watched]);

  const saveFolder = (event: FormEvent) => {
    event.preventDefault();
    attempt(async () => {
      await api.saveSettings({ mega_folder: folder ?? data?.folder ?? "" });
      setFolder(undefined);
      notify("MEGA folder saved");
      info.reload();
    });
  };
  const start = (direction: SyncDirection, withDeletes: boolean, dryRun: boolean) =>
    attempt(async () => {
      const job = await api.startSync(direction, withDeletes && direction !== "both", dryRun);
      setActive(job);
      track(job, { done: dryRun ? "Preview ready" : "Sync finished", onDone: () => info.reload() });
    });

  return (
    <>
      <PageHeader
        title="MEGA sync"
        lead="Keep a copy of the library, with its covers and details, in a folder on MEGA."
      />
      {info.error && <Problem>The sync status could not be loaded: {info.error}</Problem>}
      {!data && !info.error && <p className="text-soft">Loading…</p>}
      {data && (
        <>
          <Section title="Account">
            <Account info={data} onChanged={info.reload} />
          </Section>

          <Section title="Folder">
            <form onSubmit={saveFolder} className="flex max-w-xl flex-wrap items-end gap-3">
              <Field
                label="MEGA folder"
                placeholder="Books/Library"
                hint="A path from the top of your Cloud Drive. It is created on the first upload."
                className="min-w-[16rem] flex-1"
                value={folder ?? data.folder}
                onChange={(event) => setFolder(event.target.value)}
              />
              <Button type="submit" className="mb-[1.65rem]">
                Save folder
              </Button>
            </form>
          </Section>

          <Section title="Sync now">
            <div className="mb-5 space-y-2">
              <label className="flex items-start gap-2.5">
                <input
                  type="checkbox"
                  className="mt-1 h-4 w-4 accent-ink"
                  checked={preview}
                  onChange={(event) => setPreview(event.target.checked)}
                />
                <span>Preview only, change nothing</span>
              </label>
              <label className="flex items-start gap-2.5">
                <input
                  type="checkbox"
                  className="mt-1 h-4 w-4 accent-ink"
                  checked={mirror}
                  onChange={(event) => setMirror(event.target.checked)}
                />
                <span>
                  When uploading or downloading, also delete what the other side no longer has
                  <span className="block text-[0.9rem] text-soft">
                    Deletions on MEGA go to its Rubbish Bin. Deletions in this library are permanent.
                  </span>
                </span>
              </label>
            </div>
            <ul className="max-w-[62ch] divide-y divide-rule border-y border-rule">
              {directions.map(({ direction, label, about }, index) => (
                <li key={direction} className="flex flex-wrap items-center gap-x-6 gap-y-2 py-3.5">
                  <p className="min-w-[14rem] flex-1 text-soft">{about}</p>
                  <Button variant={index === 0 ? "primary" : "secondary"} disabled={!ready} onClick={() => start(direction, mirror, preview)}>
                    {label}
                  </Button>
                </li>
              ))}
            </ul>
            {!data.signed_in && <p className="mt-3 text-soft">Sign in to MEGA above to turn these on.</p>}
            {data.signed_in && !data.folder && <p className="mt-3 text-soft">Save a MEGA folder above to turn these on.</p>}
            {data.pending_deletions > 0 && (
              <p className="mt-3 max-w-[62ch] text-soft">
                {count(data.pending_deletions, "file")} deleted or renamed here will leave MEGA with the next upload. A
                preview shows which books they belong to.
              </p>
            )}
          </Section>

          {running && (
            <Section title="Syncing now">
              <Progress job={active ?? (lastRun as Job)} />
            </Section>
          )}
          {!running && lastRun && (
            <Section title={(lastRun.result as SyncReport | undefined)?.dry_run ? "Preview" : "Last sync"}>
              <LastRun
                job={lastRun}
                onApply={ready ? (report) => start(report.direction, report.delete, false) : undefined}
              />
            </Section>
          )}
        </>
      )}
    </>
  );
}
