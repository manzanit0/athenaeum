import { createContext, type ReactNode, useCallback, useContext, useMemo, useRef, useState } from "react";
import { api, type Job } from "./api";
import { messageOf } from "./format";

/** The mood of a notice. */
type Tone = "working" | "success" | "error";

/** A short message shown at the bottom of the screen. */
interface Notice {
  id: number;
  tone: Tone;
  text: string;
}

/** What to do while and after a background job runs. */
interface TrackOptions {
  done: string | ((job: Job) => string);
  onDone?: (job: Job) => void;
}

/** What to say while and after several background jobs run as one piece of work. */
interface TrackAllOptions {
  working: string;
  done: string;
  onDone?: () => void;
}

/** The functions pages use to report progress to the reader. */
interface Notices {
  notify: (text: string, tone?: Tone) => void;
  track: (job: Job, options: TrackOptions) => void;
  trackAll: (jobs: Job[], options: TrackAllOptions) => void;
  attempt: (action: () => Promise<unknown>) => Promise<boolean>;
}

const NoticesContext = createContext<Notices | null>(null);

const pollInterval = 600;
const successLifetime = 5000;

const toneStyles: Record<Tone, string> = {
  working: "border-ink bg-sheet text-ink",
  success: "border-leaf bg-sheet text-ink",
  error: "border-ribbon bg-sheet text-ink",
};

const toneMarks: Record<Tone, string> = {
  working: "bg-soft animate-pulse",
  success: "bg-leaf",
  error: "bg-ribbon",
};

/** Provides notices and background job tracking to the application. */
export function NoticesProvider({ children }: { children: ReactNode }) {
  const [notices, setNotices] = useState<Notice[]>([]);
  const nextId = useRef(1);

  const dismiss = useCallback((id: number) => {
    setNotices((current) => current.filter((notice) => notice.id !== id));
  }, []);

  const show = useCallback(
    (id: number, tone: Tone, text: string) => {
      setNotices((current) => [...current.filter((notice) => notice.id !== id), { id, tone, text }]);
      if (tone === "success") {
        window.setTimeout(() => dismiss(id), successLifetime);
      }
    },
    [dismiss],
  );

  const value = useMemo<Notices>(() => {
    const notify = (text: string, tone: Tone = "success") => show(nextId.current++, tone, text);
    const track = (job: Job, options: TrackOptions) => {
      const id = nextId.current++;
      show(id, "working", `${job.title}…`);
      const poll = async () => {
        try {
          const current = await api.job(job.id);
          if (current.status === "running") {
            window.setTimeout(poll, pollInterval);
            return;
          }
          if (current.status === "failed") {
            show(id, "error", `${job.title} failed: ${current.error}`);
          } else {
            show(id, "success", typeof options.done === "string" ? options.done : options.done(current));
          }
          options.onDone?.(current);
        } catch (reason) {
          show(id, "error", messageOf(reason));
        }
      };
      window.setTimeout(poll, pollInterval / 3);
    };
    const trackAll = (jobs: Job[], options: TrackAllOptions) => {
      const id = nextId.current++;
      show(id, "working", `${options.working}…`);
      const poll = async () => {
        try {
          const current = await Promise.all(jobs.map((job) => api.job(job.id)));
          if (current.some((job) => job.status === "running")) {
            window.setTimeout(poll, pollInterval);
            return;
          }
          const failed = current.filter((job) => job.status === "failed");
          if (failed.length > 0) {
            const others = failed.length > 1 ? `, and ${failed.length - 1} more failed` : "";
            show(id, "error", `${failed[0].title} failed: ${failed[0].error}${others}`);
          } else {
            show(id, "success", options.done);
          }
          options.onDone?.();
        } catch (reason) {
          show(id, "error", messageOf(reason));
        }
      };
      window.setTimeout(poll, pollInterval / 3);
    };
    const attempt = async (action: () => Promise<unknown>) => {
      try {
        await action();
        return true;
      } catch (reason) {
        notify(messageOf(reason), "error");
        return false;
      }
    };
    return { notify, track, trackAll, attempt };
  }, [show]);

  return (
    <NoticesContext.Provider value={value}>
      {children}
      <div className="pointer-events-none fixed inset-x-0 bottom-0 z-50 flex flex-col items-center gap-2 p-4">
        {notices.map((notice) => (
          <div
            key={notice.id}
            role={notice.tone === "error" ? "alert" : "status"}
            className={`toast pointer-events-auto flex w-full max-w-md items-start gap-3 rounded-sm border px-4 py-3 shadow-[0_6px_0_-3px_rgb(27_33_30/0.18)] ${toneStyles[notice.tone]}`}
          >
            <span className={`mt-1.5 h-2 w-2 shrink-0 rounded-full ${toneMarks[notice.tone]}`} aria-hidden />
            <p className="min-w-0 flex-1 break-words">{notice.text}</p>
            {notice.tone !== "working" && (
              <button
                type="button"
                className="shrink-0 text-soft underline underline-offset-2 hover:text-ink"
                onClick={() => dismiss(notice.id)}
              >
                Dismiss
              </button>
            )}
          </div>
        ))}
      </div>
    </NoticesContext.Provider>
  );
}

/** Returns the functions used to report progress to the reader. */
export function useNotices(): Notices {
  const notices = useContext(NoticesContext);
  if (!notices) {
    throw new Error("useNotices must be used inside NoticesProvider");
  }
  return notices;
}
