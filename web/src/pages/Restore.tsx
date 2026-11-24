import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { api, type Job, restoreProgressOf, restoreReportOf, type RestoreOutlook, type RestoreProgress, type RestoreReport } from "../api";
import { byline, count, dollars, sentence } from "../format";
import { useLoad } from "../hooks";
import { useNotices } from "../notices";
import { Link } from "../router";
import { Button, buttonClass, Field, PageHeader, Problem, Section } from "../ui";

const refreshInterval = 1500;

/** How many doubtful passages are listed before the rest are put behind "Show all". */
const doubtsAtFirst = 8;

/** The stages of a restore, in order, as the page words them. */
const stages: { stage: RestoreProgress["stage"]; label: string }[] = [
  { stage: "sample", label: "Reading the first pages" },
  { stage: "pages", label: "Reading the rest of the book" },
  { stage: "headings", label: "Standardising headings and capitals" },
  { stage: "writing", label: "Building the contents and writing the EPUB" },
];

/** A figure with what it counts above it. */
function Figure({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="rounded-sm border border-rule bg-sheet px-3 py-2.5">
      <dt className="text-[0.87rem] text-soft">{label}</dt>
      <dd className="font-display text-[1.35rem] font-medium tabular-nums">{children}</dd>
    </div>
  );
}

/** How far a running restore has come, stage by stage. */
function Running({ job }: { job: Job }) {
  const progress = restoreProgressOf(job);
  const at = stages.findIndex((entry) => entry.stage === progress?.stage);
  return (
    <Section title="Restoring">
      <ol className="max-w-[40rem]">
        {stages.map((entry, index) => (
          <li
            key={entry.stage}
            aria-current={index === at ? "step" : undefined}
            className={`flex items-baseline gap-3 py-1.5 ${index > at ? "text-soft" : ""}`}
          >
            <span
              aria-hidden
              className={`h-2.5 w-2.5 shrink-0 rounded-full ${
                index < at ? "bg-leaf" : index === at ? "animate-pulse bg-ink" : "border border-soft"
              }`}
            />
            <span className="flex-1">{entry.label}</span>
            {index === at && progress && index < 2 && (
              <span className="text-soft tabular-nums">
                {progress.read} of {progress.pages} pages
              </span>
            )}
          </li>
        ))}
      </ol>
      <p role="status" className="mt-3 text-soft">
        {progress ? `${dollars(progress.cost)} so far. ` : ""}It runs to the end without you: you can leave this page, and
        Activity shows how far it has come.
      </p>
    </Section>
  );
}

/** What a finished restore did, with the passages it was not sure of. */
function Outcome({ id, report }: { id: number; report: RestoreReport }) {
  const [unfolded, setUnfolded] = useState(false);
  const listed = unfolded ? report.doubts : report.doubts.slice(0, doubtsAtFirst);
  return (
    <Section title="The book has been restored">
      <p className="max-w-[62ch] text-soft">The EPUB is with the book's files now, and the PDF is as it was.</p>
      <dl className="my-4 grid grid-cols-[repeat(auto-fit,minmax(8.5rem,1fr))] gap-2.5">
        <Figure label="Pages read">{report.pages}</Figure>
        <Figure label="Chapters">{report.sections}</Figure>
        <Figure label="Headings standardised">{report.headings_changed}</Figure>
        <Figure label="Notes linked">{report.notes_linked}</Figure>
        <Figure label="Passages to check">{report.doubts.length}</Figure>
        <Figure label="Cost">{dollars(report.cost)}</Figure>
      </dl>
      {report.warnings.map((warning) => (
        <p key={warning} className="mb-2 max-w-[62ch] text-soft">
          {warning}
        </p>
      ))}
      {report.doubts.length > 0 && (
        <>
          <h3 className="mt-5 font-medium">Passages that could not be read with certainty</h3>
          <ul aria-label="Passages to check" className="mt-1 divide-y divide-rule border-y border-rule">
            {listed.map((doubt, index) => (
              <li key={`${doubt.page}-${index}`} className="flex flex-wrap items-baseline gap-x-5 gap-y-0.5 py-2">
                <span className="w-20 text-soft tabular-nums">Page {doubt.page}</span>
                <span className="min-w-[10rem] flex-1">“{doubt.text}”</span>
                <span className="min-w-[10rem] flex-1 text-soft">{doubt.reason}</span>
              </li>
            ))}
          </ul>
          {report.doubts.length > doubtsAtFirst && (
            <button
              type="button"
              aria-expanded={unfolded}
              onClick={() => setUnfolded(!unfolded)}
              className="mt-2 rounded-sm font-medium underline underline-offset-4 hover:text-ribbon-deep"
            >
              {unfolded ? `Show the first ${doubtsAtFirst} only` : `Show all ${report.doubts.length}`}
            </button>
          )}
        </>
      )}
      <div className="mt-5 flex flex-wrap gap-3">
        <Link to={`/books/${id}/preview`} className={buttonClass("primary")}>
          Read it
        </Link>
        <Link to={`/books/${id}`} className={buttonClass()}>
          Back to the book
        </Link>
      </div>
    </Section>
  );
}

/** What a restore would do and cost, and the button that starts it. */
function Offer({
  id,
  outlook,
  again,
  onStarted,
  onKeySaved,
}: {
  id: number;
  outlook: RestoreOutlook;
  again: boolean;
  onStarted: (job: Job) => void;
  onKeySaved: () => void;
}) {
  const { attempt, notify } = useNotices();
  const [key, setKey] = useState("");
  const saveKey = (event: FormEvent) => {
    event.preventDefault();
    attempt(async () => {
      await api.saveModelKey(key);
      setKey("");
      notify("Key saved");
      onKeySaved();
    });
  };
  const start = () => attempt(async () => onStarted(await api.restore(id, outlook.replaces)));
  const steps = [
    outlook.scans > 0 && {
      title: "Read the scanned pages into text",
      detail: `${count(outlook.scans, "page")}${outlook.text > 0 ? `, and ${count(outlook.text, "page")} that already ${outlook.text === 1 ? "has" : "have"} text` : ""}.`,
    },
    outlook.scans === 0 && {
      title: "Read the pages into clean text",
      detail: `${count(outlook.text, "page")}, without the running headers, footers and page numbers.`,
    },
    { title: "Build the contents and link the notes", detail: "A chapter for every chapter heading, and every note reference leading to its note." },
    { title: "Standardise headings and capitals", detail: "One style for every level of heading. The words stay as written." },
  ].filter((step) => step !== false);
  return (
    <Section title={again ? "Restore it again" : "What it does"}>
      <ol className="max-w-[62ch] list-decimal space-y-2 pl-5">
        {steps.map((step) => (
          <li key={step.title}>
            {step.title}
            <p className="text-[0.93rem] text-soft">{step.detail}</p>
          </li>
        ))}
      </ol>
      <dl className="my-5 grid max-w-[40rem] grid-cols-[repeat(auto-fit,minmax(9rem,1fr))] gap-2.5">
        <Figure label="Pages to send">{outlook.scans + outlook.text}</Figure>
        <Figure label="Estimated cost">
          {dollars(outlook.cost_low)} to {dollars(outlook.cost_high)}
        </Figure>
        <Figure label="Stops if it reaches">{dollars(outlook.limit)}</Figure>
      </dl>
      <div className="max-w-[62ch] space-y-2 text-soft">
        <p>
          It reads the first {count(outlook.sample, "page")} and ends there, for the cost of those pages, if the book
          would not come out well. Otherwise it runs to the end without you and adds the EPUB to the book.
        </p>
        <p>The pages are sent to Anthropic to be read. The PDF is kept as it is.</p>
        {outlook.replaces && <p className="text-ink">This book already has an EPUB. The restored one takes its place.</p>}
      </div>
      {!outlook.configured && (
        <form onSubmit={saveKey} className="mt-5 flex max-w-[34rem] flex-wrap items-end gap-3">
          <Field
            label="Anthropic API key"
            hint="Needed once. It is kept encrypted with this library."
            type="password"
            autoComplete="off"
            value={key}
            onChange={(event) => setKey(event.target.value)}
            className="min-w-[16rem] flex-1"
          />
          <Button type="submit" disabled={!key.trim()} className="mb-[1.55rem]">
            Save the key
          </Button>
        </form>
      )}
      <div className="mt-5 flex flex-wrap gap-3">
        <Button variant="primary" onClick={start} disabled={!outlook.configured}>
          {outlook.replaces ? "Restore and replace the EPUB" : "Restore the book"}
        </Button>
        <Link to={`/books/${id}/edit`} className={buttonClass()}>
          Cancel
        </Link>
      </div>
    </Section>
  );
}

/**
 * The page that restores a book: what it would do and cost before it
 * starts, how far it has come while it runs, and what it did once it is
 * done. A restore runs from start to end by itself.
 */
export function RestorePage({ id }: { id: number }) {
  const book = useLoad(() => api.book(id), [id]);
  const outlook = useLoad(() => api.restoreOutlook(id), [id]);
  const jobs = useLoad(api.jobs, []);
  const [started, setStarted] = useState<Job>();
  const latest = jobs.data?.find((job) => job.kind === "restore" && job.book_id === id);
  // The job just started stands in until the list of jobs has it.
  const job = latest && (!started || latest.id >= started.id) ? latest : started;
  const running = job?.status === "running";
  useEffect(() => {
    if (!running) {
      return;
    }
    const timer = window.setInterval(jobs.reload, refreshInterval);
    return () => window.clearInterval(timer);
  }, [running, jobs.reload]);
  // Once a restore has ended, what restoring again would involve has changed: the book has an EPUB now.
  const status = job?.status;
  useEffect(() => {
    if (status && status !== "running") {
      outlook.reload();
    }
  }, [status, outlook.reload]);
  const report = job && restoreReportOf(job);
  const title = book.data ? `Restore “${book.data.title}”` : "Restore";
  return (
    <>
      <PageHeader
        title={title}
        lead={
          book.data && (
            <>
              {byline(book.data.authors)}
              {book.data.authors.length > 0 && " · "}
              <Link to={`/books/${id}`} className="underline underline-offset-4 hover:text-ribbon-deep">
                Back to the book
              </Link>
            </>
          )
        }
      />
      {book.error && <Problem>The book could not be loaded: {book.error}</Problem>}
      {outlook.error && <Problem>What a restore would involve could not be worked out: {outlook.error}</Problem>}
      {job && running && <Running job={job} />}
      {job?.status === "failed" && (
        <div className="mb-8">
          <Problem>{sentence(job.error)} Nothing was added to the book.</Problem>
        </div>
      )}
      {report && <Outcome id={id} report={report} />}
      {outlook.data && !outlook.data.possible && <Problem>{outlook.data.reason}</Problem>}
      {outlook.data?.possible && !running && (
        <Offer
          id={id}
          outlook={outlook.data}
          again={job !== undefined}
          onStarted={(next) => {
            setStarted(next);
            jobs.reload();
          }}
          onKeySaved={outlook.reload}
        />
      )}
    </>
  );
}
