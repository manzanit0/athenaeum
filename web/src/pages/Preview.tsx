import { useEffect, useRef, useState } from "react";
import { api, type Book, isJob, type Job, type Preview } from "../api";
import { byline, messageOf } from "../format";
import { useLoad } from "../hooks";
import { Link } from "../router";
import { Button, Problem } from "../ui";

const sizes = [85, 100, 115, 130, 150, 175];
const sizeKey = "athenaeum.preview.size";
const pollInterval = 600;
const frameClass = "h-[calc(100dvh-19rem)] min-h-[26rem] w-full rounded-sm border border-rule bg-sheet";
const selectClass = "max-w-full rounded-sm border border-rule bg-sheet px-3 py-2";

/** Returns the text size the reader last chose. */
function storedSize(): number {
  try {
    const stored = Number(window.localStorage.getItem(sizeKey));
    return sizes.includes(stored) ? stored : 100;
  } catch {
    return 100;
  }
}

/** Remembers the reader's text size, and returns it with its setter. */
function useTextSize(): [number, (step: number) => void] {
  const [size, setSize] = useState(storedSize);
  const change = (step: number) => {
    const next = sizes[sizes.indexOf(size) + step] ?? size;
    setSize(next);
    try {
      window.localStorage.setItem(sizeKey, String(next));
    } catch {
      return;
    }
  };
  return [size, change];
}

/** Returns the value of one of the application's colour tokens. */
function token(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(`--color-${name}`).trim();
}

/** Returns the style that lays a book's page out for reading. */
function pageStyle(size: number): string {
  return `
    html { font-size: ${size}% !important; background: ${token("sheet")}; }
    body {
      box-sizing: border-box; max-width: 36em; margin: 0 auto !important;
      padding: 2.5rem 1.5rem 5rem !important; color: ${token("ink")}; line-height: 1.6;
      font-family: "Iowan Old Style", "Palatino Linotype", Palatino, Georgia, serif;
      overflow-wrap: break-word;
    }
    img, svg, video { max-width: 100%; height: auto; }
    a { color: ${token("ribbon-deep")}; }
  `;
}

/** Adds or updates the reading style in a page of the book. */
function applyStyle(frame: HTMLIFrameElement | null, size: number): void {
  try {
    const doc = frame?.contentDocument;
    if (!doc?.documentElement) {
      return;
    }
    let style = doc.getElementById("athenaeum-reading");
    if (!style) {
      style = doc.createElement("style");
      style.id = "athenaeum-reading";
      (doc.head ?? doc.documentElement).appendChild(style);
    }
    style.textContent = pageStyle(size);
  } catch {
    return;
  }
}

/** Returns the path within the book that the frame is showing. */
function shownPath(frame: HTMLIFrameElement | null, base: string): string | undefined {
  try {
    const path = frame?.contentWindow?.location.pathname;
    return path?.startsWith(base) ? decodeURIComponent(path.slice(base.length)) : undefined;
  } catch {
    return undefined;
  }
}

/** The buttons that change the text size. */
function TextSize({ size, onChange }: { size: number; onChange: (step: number) => void }) {
  return (
    <div className="flex gap-2">
      <Button aria-label="Smaller text" disabled={size === sizes[0]} onClick={() => onChange(-1)}>
        <span aria-hidden className="text-[0.85em]">
          A
        </span>
      </Button>
      <Button aria-label="Larger text" disabled={size === sizes[sizes.length - 1]} onClick={() => onChange(1)}>
        <span aria-hidden className="text-[1.2em] leading-none">
          A
        </span>
      </Button>
    </div>
  );
}

/** Shows an EPUB one section at a time. */
function EpubReader({ preview, title }: { preview: Preview; title: string }) {
  const frame = useRef<HTMLIFrameElement>(null);
  const [first] = useState(() => preview.base + preview.sections[0].href);
  const [current, setCurrent] = useState(0);
  const [picked, setPicked] = useState(-1);
  const [size, changeSize] = useTextSize();
  const lines = preview.contents.length
    ? preview.contents
    : preview.sections.map((section, index) => ({ title: `Section ${index + 1}`, href: section.href, level: 0 }));
  const paths = preview.sections.map((section) => decodeURIComponent(section.href));
  const here = decodeURIComponent(preview.sections[current].href);
  const isHere = (index: number) => decodeURIComponent(lines[index].href.split("#")[0]) === here;
  const line = picked >= 0 && isHere(picked) ? picked : lines.findIndex((_, index) => isHere(index));

  const open = (href: string) => frame.current?.contentWindow?.location.replace(preview.base + href);
  const pick = (index: number) => {
    setPicked(index);
    open(lines[index].href);
  };
  const loaded = () => {
    applyStyle(frame.current, size);
    const index = paths.indexOf(shownPath(frame.current, preview.base) ?? "");
    if (index >= 0) {
      setCurrent(index);
    }
  };
  useEffect(() => applyStyle(frame.current, size), [size]);

  return (
    <>
      <div className="mb-3 flex flex-wrap items-end gap-x-4 gap-y-3">
        <div className="min-w-0 flex-1 basis-56">
          <label htmlFor="preview-contents" className="mb-1 block font-medium">
            Contents
          </label>
          <select
            id="preview-contents"
            className={`${selectClass} w-full`}
            value={line}
            onChange={(event) => pick(Number(event.target.value))}
          >
            {line < 0 && <option value={-1}>Section {current + 1}</option>}
            {lines.map((entry, index) => (
              <option key={index} value={index}>
                {"  ".repeat(entry.level) + (entry.title || `Section ${index + 1}`)}
              </option>
            ))}
          </select>
        </div>
        <Button disabled={current === 0} onClick={() => open(preview.sections[current - 1].href)}>
          Previous
        </Button>
        <Button disabled={current >= preview.sections.length - 1} onClick={() => open(preview.sections[current + 1].href)}>
          Next
        </Button>
        <TextSize size={size} onChange={changeSize} />
      </div>
      <iframe ref={frame} title={title} src={first} sandbox="allow-same-origin" onLoad={loaded} className={frameClass} />
      <p className="mt-2 text-[0.87rem] text-soft tabular-nums">
        Section {current + 1} of {preview.sections.length}
      </p>
    </>
  );
}

/** Shows a plain text book. */
function TextReader({ preview }: { preview: Preview }) {
  const [size, changeSize] = useTextSize();
  const text = useLoad(() => fetch(preview.base).then((response) => response.text()), [preview.base]);
  if (text.error) {
    return <Problem>The text could not be loaded: {text.error}</Problem>;
  }
  return (
    <>
      <div className="mb-3 flex justify-end">
        <TextSize size={size} onChange={changeSize} />
      </div>
      <div className={`${frameClass} overflow-y-auto`} tabIndex={0}>
        <pre
          className="mx-auto max-w-[36em] whitespace-pre-wrap px-6 pb-16 pt-9 font-display leading-[1.6] [overflow-wrap:anywhere]"
          style={{ fontSize: `${size}%` }}
        >
          {text.data ?? "Opening the book…"}
        </pre>
      </div>
    </>
  );
}

/** Waits for the job preparing a preview and reports how it ended. */
function Preparing({ job, format, onDone }: { job: Job; format: string; onDone: () => void }) {
  const [error, setError] = useState("");
  useEffect(() => {
    let timer = 0;
    const poll = async () => {
      try {
        const current = await api.job(job.id);
        if (current.status === "running") {
          timer = window.setTimeout(poll, pollInterval);
        } else if (current.status === "failed") {
          setError(current.error);
        } else {
          onDone();
        }
      } catch (reason) {
        setError(messageOf(reason));
      }
    };
    timer = window.setTimeout(poll, pollInterval / 3);
    return () => window.clearTimeout(timer);
  }, [job.id]);
  if (error) {
    return <Problem>The preview could not be prepared: {error}</Problem>;
  }
  return (
    <p role="status" className="text-soft">
      Converting the {format ? `${format.toUpperCase()} file` : "book"} so it can be shown here. This can take a minute…
    </p>
  );
}

/** Chooses the reader that fits the preview. */
function Reader({ preview, title }: { preview: Preview; title: string }) {
  switch (preview.kind) {
    case "pdf":
      return (
        <>
          <iframe title={title} src={preview.base} className={frameClass} />
          <p className="mt-2 text-[0.87rem] text-soft">
            Not every browser shows a PDF inside a page.{" "}
            <a href={preview.base} target="_blank" rel="noreferrer" className="underline underline-offset-2 hover:text-ink">
              Open the PDF in a new tab
            </a>
          </p>
        </>
      );
    case "text":
      return <TextReader preview={preview} />;
    default:
      return <EpubReader key={preview.base} preview={preview} title={title} />;
  }
}

/** The file picker and the reader for one book. */
function BookPreview({ book }: { book: Book }) {
  const [format, setFormat] = useState("");
  const answer = useLoad(() => api.preview(book.id, format), [book.id, format]);
  const shown = answer.data && !isJob(answer.data) && !answer.error ? answer.data.format : format;
  return (
    <>
      {book.formats.length > 1 && (
        <div className="mb-4 flex items-center gap-3">
          <label htmlFor="preview-file" className="font-medium">
            File
          </label>
          <select id="preview-file" className={selectClass} value={shown} onChange={(event) => setFormat(event.target.value)}>
            {!shown && <option value="">Choosing…</option>}
            {book.formats.map((file) => (
              <option key={file.format} value={file.format}>
                {file.format.toUpperCase()}
              </option>
            ))}
          </select>
        </div>
      )}
      {answer.error && <Problem>This book cannot be previewed: {answer.error}</Problem>}
      {!answer.error && !answer.data && <p className="text-soft">Opening the book…</p>}
      {!answer.error && answer.data && isJob(answer.data) && (
        <Preparing key={answer.data.id} job={answer.data} format={format} onDone={answer.reload} />
      )}
      {!answer.error && answer.data && !isJob(answer.data) && <Reader preview={answer.data} title={book.title} />}
    </>
  );
}

/** A book opened for reading in the browser. */
export function PreviewPage({ id }: { id: number }) {
  const book = useLoad(() => api.book(id), [id]);
  if (book.error) {
    return (
      <>
        <Problem>This book could not be opened: {book.error}</Problem>
        <Link to="/" className="mt-4 inline-block underline underline-offset-4">
          Back to the library
        </Link>
      </>
    );
  }
  if (!book.data) {
    return <p className="text-soft">Opening the book…</p>;
  }
  return (
    <>
      <Link to={`/books/${id}`} className="text-soft underline underline-offset-4 hover:text-ink">
        Back to the book
      </Link>
      <header className="mb-5 mt-3 border-b border-rule pb-4">
        <h1 className="font-display text-[1.7rem] font-medium leading-tight tracking-[-0.01em]">{book.data.title}</h1>
        <p className="mt-1 text-soft">{byline(book.data.authors)}</p>
      </header>
      <BookPreview book={book.data} />
    </>
  );
}
