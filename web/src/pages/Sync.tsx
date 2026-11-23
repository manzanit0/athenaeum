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
          removalNote={
            report.direction === "both"
              ? "What is removed from this library is kept in its trash folder."
              : "What is removed from this library is deleted for good."
          }
        />
      )}
      {report?.conflicts && report.conflicts.length > 0 && (
        <div className="mt-4 border-t border-rule pt-4">
          <p>
            {count(report.conflicts.length, "file")} changed both here and on MEGA since the last sync. The newer
            version {planned ? "stays" : "was kept"}; the other {planned ? "goes" : "went"} to the trash folder or to
            MEGA's Rubbish Bin.
          </p>
          <ul className="mt-1 text-[0.9rem] text-soft">
            {report.conflicts.map((c) => (
              <li key={c.path} className="truncate">
                {c.path}: {c.kept === "library" ? "this library's version" : "MEGA's version"}
              </li>
            ))}
          </ul>
        </div>
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

/** A chevron that points right, or down when what it stands before is open. */
function Chevron({ open }: { open: boolean }) {
  return (
    <svg
      aria-hidden
      viewBox="0 0 12 12"
      className={`h-3 w-3 shrink-0 transition-transform duration-150 ${open ? "" : "-rotate-90"}`}
      fill="none"
      stroke="currentColor"
    >
      <path d="M2.5 4.5 6 8l3.5-3.5" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/**
 * Keeps the library in step with a folder on MEGA. Once an account and
 * a folder are set, they are said in a sentence and one button syncs.
 * Every sync first shows what it would change, and changes nothing
 * until that is confirmed. Syncing one way only, and deleting what the
 * other side no longer has, are kept behind "More options".
 */
export function SyncPage() {
  const { notify, track, attempt } = useNotices();
  const info = useLoad(api.sync, []);
  const [folder, setFolder] = useState<string>();
  const [mirror, setMirror] = useState(false);
  const [changing, setChanging] = useState(false);
  const [options, setOptions] = useState(false);
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
      setChanging(false);
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
          {data.signed_in && data.folder && !changing ? (
            <p className="mb-8 max-w-[62ch]">
              Signed in to MEGA as {data.account}, syncing with the folder{" "}
              <span className="font-medium">{data.folder}</span>.{" "}
              <Button variant="quiet" className="py-0 font-normal" onClick={() => setChanging(true)}>
                Change
              </Button>
            </p>
          ) : (
            <>
              <Section title="Account">
                <Account info={data} onChanged={info.reload} />
              </Section>

              <Section title="Folder">
                <form onSubmit={saveFolder} className="flex max-w-2xl flex-wrap items-end gap-3">
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
                  {changing && data.signed_in && data.folder && (
                    <Button
                      variant="quiet"
                      className="mb-[1.65rem] ml-2"
                      onClick={() => {
                        setFolder(undefined);
                        setChanging(false);
                      }}
                    >
                      Done
                    </Button>
                  )}
                </form>
              </Section>
            </>
          )}

          <div className="mb-10">
            <div className="flex max-w-[62ch] flex-wrap items-center gap-x-5 gap-y-3">
              <Button
                variant="primary"
                className="px-6 py-2.5 text-[1.05rem]"
                disabled={!ready}
                onClick={() => start("both", false, true)}
              >
                Sync
              </Button>
              <p className="min-w-[14rem] flex-1 text-soft">
                Compares this library with MEGA and shows what would change. Nothing changes until you confirm.
              </p>
            </div>
            <button
              type="button"
              aria-expanded={options}
              aria-controls="sync-options"
              onClick={() => setOptions(!options)}
              className="mt-4 inline-flex items-center gap-2 text-soft hover:text-ink"
            >
              <Chevron open={options} />
              More options
            </button>
            {options && (
              <div id="sync-options" className="mt-3 max-w-[62ch] border-l-4 border-rule bg-sheet px-5 py-4">
                <p className="font-medium">Sync one way only</p>
                <p className="mt-0.5 text-[0.9rem] text-soft">
                  Uploading makes the MEGA folder catch up with this library; downloading makes this library catch up
                  with the MEGA folder. Each shows what it would change first.
                </p>
                <div className="mt-3 flex flex-wrap gap-3">
                  <Button disabled={!ready} onClick={() => start("push", mirror, true)}>
                    Upload to MEGA
                  </Button>
                  <Button disabled={!ready} onClick={() => start("pull", mirror, true)}>
                    Download from MEGA
                  </Button>
                </div>
                <label className="mt-4 flex items-start gap-2.5">
                  <input
                    type="checkbox"
                    className="mt-1 h-4 w-4 accent-ink"
                    checked={mirror}
                    onChange={(event) => setMirror(event.target.checked)}
                  />
                  <span>
                    Also delete what the other side no longer has
                    <span className="block text-[0.9rem] text-soft">
                      Deletions on MEGA go to its Rubbish Bin. A sync keeps what it removes from this library in the
                      library's trash folder; a download with the delete option deletes for good.
                    </span>
                  </span>
                </label>
              </div>
            )}
            {!data.signed_in && <p className="mt-3 text-soft">Sign in to MEGA above to turn this on.</p>}
            {data.signed_in && !data.folder && <p className="mt-3 text-soft">Save a MEGA folder above to turn this on.</p>}
            {data.pending_deletions > 0 && (
              <p className="mt-4 max-w-[62ch] text-soft">
                {count(data.pending_deletions, "file")} deleted or renamed here will leave MEGA with the next sync.
              </p>
            )}
          </div>

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
