import { useEffect } from "react";
import { api, type Job, warningsOf } from "../api";
import { dateTime } from "../format";
import { useLoad } from "../hooks";
import { Link } from "../router";
import { Empty, PageHeader, Problem } from "../ui";

const refreshInterval = 1500;

const statusLabels: Record<Job["status"], string> = { running: "Running", done: "Done", failed: "Failed" };

const statusStyles: Record<Job["status"], string> = {
  running: "bg-soft animate-pulse",
  done: "bg-leaf",
  failed: "bg-ribbon",
};

/** The history of conversions, transfers and syncs. */
export function ActivityPage() {
  const jobs = useLoad(api.jobs, []);
  const busy = jobs.data?.some((job) => job.status === "running") ?? false;
  useEffect(() => {
    if (!busy) {
      return;
    }
    const timer = window.setInterval(jobs.reload, refreshInterval);
    return () => window.clearInterval(timer);
  }, [busy, jobs.reload]);
  return (
    <>
      <PageHeader title="Activity" lead="Conversions, Kindle transfers and syncs, newest first." />
      {jobs.error && <Problem>The activity could not be loaded: {jobs.error}</Problem>}
      {jobs.data?.length === 0 && (
        <Empty title="Nothing has run yet">Convert a book, send one to the Kindle or sync with MEGA.</Empty>
      )}
      <ul className="divide-y divide-rule">
        {jobs.data?.map((job) => (
          <li key={job.id} className="flex flex-wrap items-baseline gap-x-5 gap-y-1 py-3">
            <span className="flex w-24 items-center gap-2">
              <span className={`h-2 w-2 rounded-full ${statusStyles[job.status]}`} aria-hidden />
              {statusLabels[job.status]}
            </span>
            <div className="min-w-[14rem] flex-1">
              {job.book_id ? (
                <Link to={`/books/${job.book_id}`} className="hover:underline">
                  {job.title}
                </Link>
              ) : (
                job.title
              )}
              {job.error && <p className="text-[0.9rem] text-ribbon-deep">{job.error}</p>}
              {warningsOf(job).length > 0 && (
                <ul aria-label="Left out" className="mt-1 list-disc pl-5 text-[0.9rem] text-soft">
                  {warningsOf(job).map((warning) => (
                    <li key={warning}>{warning}</li>
                  ))}
                </ul>
              )}
            </div>
            <time className="text-soft tabular-nums">{dateTime(job.created_at)}</time>
          </li>
        ))}
      </ul>
    </>
  );
}
