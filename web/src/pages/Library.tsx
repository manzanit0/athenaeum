import { type DragEvent, useEffect, useMemo, useRef, useState } from "react";
import { api, type Book } from "../api";
import { Cover } from "../Cover";
import { byline, count, languageName } from "../format";
import { useDebounced, useLoad } from "../hooks";
import { useNotices } from "../notices";
import { Link, navigate, replace, useSearch } from "../router";
import { Selection } from "../Selection";
import {
  blank,
  type Layout,
  narrowed,
  readLayout,
  readView,
  rememberShelf,
  saveLayout,
  type View,
  viewHref,
} from "../shelf";
import { ShelfTable } from "../ShelfTable";
import { Shelves } from "../Shelves";
import { SortMenu } from "../SortMenu";
import { usable, useSystem } from "../system";
import { Button, buttonClass, Empty, Problem } from "../ui";

/** The query parameter that says the reader came to choose books for the Kindle. */
const sendParameter = "send";

/**
 * One book on the shelf. A click opens it. Its tick box shows while the
 * pointer is over the book, while it has the keyboard, and always on
 * touch screens. While books are being selected, every tick box shows
 * and a click on the book ticks it instead.
 */
function ShelfBook({
  book,
  ticked,
  selecting,
  onTick,
}: {
  book: Book;
  ticked: boolean;
  selecting: boolean;
  onTick: () => void;
}) {
  const formats = book.formats.map((f) => f.format.toUpperCase()).join(", ");
  const cover = selecting ? (ticked ? "" : "opacity-80") : "transition-transform duration-150 group-hover:-translate-y-1";
  const box = selecting
    ? ""
    : "opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100";
  return (
    <li className="group relative">
      <Link
        to={`/books/${book.id}`}
        aria-label={`${book.title} by ${byline(book.authors)}`}
        className="block"
        onClick={(event) => {
          if (selecting) {
            event.preventDefault();
            onTick();
          }
        }}
      >
        <span className={`block ${ticked ? "outline-3 outline-offset-3 outline-ribbon" : ""}`}>
          <Cover book={book} className={cover} />
        </span>
        <p className="mt-3 line-clamp-2 font-display text-[1.02rem] font-medium leading-snug">{book.title}</p>
        <p className="mt-0.5 truncate text-[0.9rem] text-soft">{byline(book.authors)}</p>
        <p className="mt-0.5 text-[0.8rem] text-soft">{formats || "No files"}</p>
      </Link>
      <input
        type="checkbox"
        checked={ticked}
        onChange={onTick}
        aria-label={`Select ${book.title}`}
        className={`absolute left-2 top-2 size-5 cursor-pointer accent-ribbon ${box}`}
      />
    </li>
  );
}

/**
 * How many books the shelf shows of how many, and what it is narrowed
 * to, each part removable on its own. Beside the shelves, which mark
 * what is chosen themselves, it is shown for a search and for an
 * author, which no shelf stands for. In a narrow window, where the
 * shelves are put away, it is shown for any narrowing.
 */
function Narrowing({ view, lead, onShow }: { view: View; lead: string; onShow: (view: View) => void }) {
  const shelved = [
    ...view.tags.map((tag) => ({ label: tag, without: { ...view, tags: view.tags.filter((entry) => entry !== tag) } })),
    ...(view.untagged ? [{ label: "Without a category", without: { ...view, untagged: false } }] : []),
    ...view.languages.map((language) => ({
      label: languageName(language),
      without: { ...view, languages: view.languages.filter((entry) => entry !== language) },
    })),
    ...view.formats.map((format) => ({
      label: format.toUpperCase(),
      without: { ...view, formats: view.formats.filter((entry) => entry !== format) },
    })),
  ].map((part) => ({ ...part, wide: false }));
  const parts = [
    ...shelved,
    ...(view.author ? [{ label: `By ${view.author}`, without: { ...view, author: "" }, wide: true }] : []),
  ];
  const wide = view.author !== "" || view.query.trim() !== "";
  return (
    <div className={`mb-6 flex flex-wrap items-center gap-x-5 gap-y-2 ${wide ? "" : "md:hidden"}`}>
      <p className="text-soft">{lead}</p>
      {parts.length > 0 && (
        <div
          role="group"
          aria-label="Showing only"
          className={`flex flex-wrap items-center gap-2 ${view.author ? "" : "md:hidden"}`}
        >
          <span className="text-soft">Showing only</span>
          {parts.map(({ label, without, wide: kept }) => (
            <button
              key={label}
              type="button"
              aria-label={`Stop narrowing to ${label}`}
              onClick={() => onShow(without)}
              className={
                "inline-flex items-center gap-2 rounded-sm border border-ink px-2.5 py-1 leading-tight hover:bg-ink/5 " +
                (kept ? "" : "md:hidden")
              }
            >
              {label}
              <span aria-hidden className="text-soft">
                ×
              </span>
            </button>
          ))}
          <Button
            variant="quiet"
            className="md:hidden"
            onClick={() => onShow({ ...blank, query: view.query, sort: view.sort })}
          >
            Show everything
          </Button>
        </div>
      )}
    </div>
  );
}

/** A window with its side panel marked: the sign of the button that shows and hides the shelves. */
function PanelIcon() {
  return (
    <svg aria-hidden viewBox="0 0 16 14" className="h-3.5 w-4 shrink-0" fill="none" stroke="currentColor" strokeWidth="1.4">
      <rect x="0.7" y="0.7" width="14.6" height="12.6" rx="1.6" />
      <path d="M5.6 0.7v12.6" />
    </svg>
  );
}

/** Four squares, the sign of books shown as covers, or three lines, the sign of books shown as a list. */
function LayoutIcon({ layout }: { layout: Layout }) {
  return (
    <svg
      aria-hidden
      viewBox="0 0 16 16"
      className="size-[1.05rem]"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
    >
      {layout === "grid" ? (
        <>
          <rect x="2" y="2" width="4.8" height="4.8" rx="0.8" />
          <rect x="9.2" y="2" width="4.8" height="4.8" rx="0.8" />
          <rect x="2" y="9.2" width="4.8" height="4.8" rx="0.8" />
          <rect x="9.2" y="9.2" width="4.8" height="4.8" rx="0.8" />
        </>
      ) : (
        <path d="M2.5 4h11M2.5 8h11M2.5 12h11" />
      )}
    </svg>
  );
}

/** Says what a run of sending books to the Kindle did. */
function sentNote(sent: Book[], already: number, empty: number): string {
  const left = [
    ...(already > 0 ? [`skipped ${already} already on it`] : []),
    ...(empty > 0 ? [`${empty} with no file that can be sent`] : []),
  ];
  if (sent.length === 0) {
    return `Nothing was sent to the Kindle: ${left.join(", ")}`;
  }
  const what = sent.length === 1 ? `“${sent[0].title}”` : count(sent.length, "book");
  return [`Sent ${what} to the Kindle`, ...left].join(", ");
}

/**
 * The shelf of the library: search it, browse it by its shelves, add
 * to it, and tick its books to file them or send them to the Kindle.
 * The shelves take the side of the window, the one panel beside the
 * books, and mark what is chosen. In a narrow window they open between
 * the controls and the books, behind a button, and what the shelf is
 * narrowed to is said above the books instead.
 */
export function LibraryPage() {
  const system = useSystem();
  const { notify, trackAll, attempt } = useNotices();
  const search = useSearch();
  const view = useMemo(() => readView(search), [search]);
  const [typed, setTyped] = useState(view.query);
  const written = useRef(view.query);
  const settled = useDebounced(typed, 200);
  const [dragging, setDragging] = useState(false);
  const [adding, setAdding] = useState(false);
  const [shelves, setShelves] = useState(false);
  const [sending] = useState(() => new URLSearchParams(window.location.search).has(sendParameter));
  const [busy, setBusy] = useState(false);
  const [ticked, setTicked] = useState<Set<number>>(new Set());
  const [layout, setLayout] = useState<Layout>(readLayout);
  const picker = useRef<HTMLInputElement>(null);
  const books = useLoad(() => api.books(view), [search]);
  const facets = useLoad(() => api.facets(view), [search]);
  const library = useLoad(() => api.facets(blank), []);
  const device = useLoad(api.device, []);

  useEffect(() => {
    if (new URLSearchParams(window.location.search).has(sendParameter)) {
      replace(viewHref(view));
    }
  }, []);
  useEffect(() => {
    if (settled !== view.query) {
      written.current = settled;
      replace(viewHref({ ...view, query: settled }));
    }
  }, [settled]);
  useEffect(() => {
    if (view.query !== written.current) {
      written.current = view.query;
      setTyped(view.query);
    }
  }, [view.query]);
  useEffect(() => rememberShelf(viewHref(view)), [view]);

  const show = (next: View) => {
    if (viewHref(next) !== viewHref(view)) {
      navigate(viewHref(next), !window.matchMedia("(min-width: 48rem)").matches);
    }
  };
  const reload = () => {
    books.reload();
    facets.reload();
    library.reload();
  };

  const add = async (files: File[]) => {
    if (files.length === 0) {
      return;
    }
    setAdding(true);
    await attempt(async () => {
      const result = await api.importBooks(files);
      if (result.books.length > 0) {
        notify(`Added ${count(result.books.length, "book")}`);
      }
      for (const failure of result.errors) {
        notify(`${failure.file} was not added: ${failure.error}`, "error");
      }
      reload();
    });
    setAdding(false);
    if (picker.current) {
      picker.current.value = "";
    }
  };

  const drop = (event: DragEvent) => {
    event.preventDefault();
    setDragging(false);
    add(Array.from(event.dataTransfer.files));
  };

  const shelf = books.data ?? [];
  const chosen = shelf.filter((book) => ticked.has(book.id));
  const selecting = chosen.length > 0 || sending;
  const tick = (id: number) => {
    const next = new Set(ticked);
    if (!next.delete(id)) {
      next.add(id);
    }
    setTicked(next);
  };
  const clear = () => setTicked(new Set());
  const tag = (put: string[], taken: string[]) =>
    attempt(async () => {
      await api.tagBooks(
        chosen.map((book) => book.id),
        put,
        taken,
      );
      const changed = count(chosen.length, "book");
      notify(put.length > 0 ? `Filed ${changed} under “${put[0]}”` : `Took ${changed} out of “${taken[0]}”`);
      reload();
    });
  const send = async () => {
    setBusy(true);
    await attempt(async () => {
      const there = new Set((await api.deviceBooks()).map((book) => book.book_id));
      const filed = chosen.filter((book) => usable(book, system));
      const ready = filed.filter((book) => !there.has(book.id));
      const note = sentNote(ready, filed.length - ready.length, chosen.length - filed.length);
      if (ready.length === 0) {
        notify(note);
        return;
      }
      const jobs = [];
      for (const book of ready) {
        jobs.push(await api.sendToDevice(book.id));
      }
      clear();
      trackAll(jobs, {
        working: `Sending ${count(ready.length, "book")} to the Kindle`,
        done: note,
        onDone: () => sending && navigate("/kindle"),
      });
    });
    setBusy(false);
  };

  useEffect(() => {
    if (chosen.length === 0) {
      return;
    }
    const keys = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        clear();
      }
    };
    document.addEventListener("keydown", keys);
    return () => document.removeEventListener("keydown", keys);
  }, [chosen.length]);

  const total = (facets.data ?? library.data)?.total ?? 0;
  const searching = view.query.trim() !== "";
  const partial = searching || narrowed(view);
  const empty = books.data !== undefined && facets.data !== undefined && total === 0;
  return (
    <div
      onDragOver={(event) => {
        event.preventDefault();
        setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={drop}
      className={
        "md:grid md:grid-rows-[auto_1fr] " +
        (total > 0 ? "md:grid-cols-[16rem_minmax(0,1fr)] " : "md:grid-cols-[minmax(0,1fr)] ") +
        (dragging ? "outline-2 -outline-offset-8 outline-dashed outline-ribbon" : "")
      }
    >
      <div className="px-5 pt-7 md:col-start-[-2] md:row-start-1 md:px-10 md:pt-9">
        <h1 className="sr-only">Library</h1>
        <div className="mb-6 flex flex-wrap items-center gap-x-4 gap-y-3">
          {total > 0 && (
            <button
              type="button"
              aria-expanded={shelves}
              aria-controls="library-shelves"
              onClick={() => setShelves(!shelves)}
              className={
                "inline-flex h-[2.6rem] items-center gap-2 rounded-sm border bg-sheet px-3.5 font-medium hover:border-ink md:hidden " +
                (shelves ? "border-ink bg-ink/[0.09]" : "border-rule")
              }
            >
              <PanelIcon />
              Filters
            </button>
          )}
          {!empty && (
            <>
              <div className="min-w-[12rem] flex-1">
                <label htmlFor="library-search" className="sr-only">
                  Search library
                </label>
                <input
                  id="library-search"
                  type="search"
                  value={typed}
                  onChange={(event) => setTyped(event.target.value)}
                  placeholder={total > 0 ? `Search ${count(total, "book")}` : "Search"}
                  className="h-[2.6rem] w-full rounded-sm border border-rule bg-sheet px-3 focus:border-ink focus:outline-none focus:ring-2 focus:ring-ribbon/40"
                />
              </div>
              <SortMenu sort={view.sort} onSort={(sort) => show({ ...view, sort })} />
              <div role="group" aria-label="Show books as" className="flex">
                {(["grid", "list"] as const).map((option) => (
                  <button
                    key={option}
                    type="button"
                    aria-pressed={layout === option}
                    aria-label={option === "grid" ? "Grid" : "List"}
                    title={option === "grid" ? "Grid" : "List"}
                    onClick={() => {
                      setLayout(option);
                      saveLayout(option);
                    }}
                    className={
                      "grid h-[2.6rem] place-items-center border px-3 first:rounded-l-sm last:-ml-px last:rounded-r-sm " +
                      (layout === option ? "relative border-ink bg-ink text-sheet" : "border-rule bg-sheet hover:border-ink")
                    }
                  >
                    <LayoutIcon layout={option} />
                  </button>
                ))}
              </div>
            </>
          )}
          <label
            className={`${buttonClass("primary")} ml-auto h-[2.6rem] cursor-pointer focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-ribbon`}
          >
            {adding ? "Adding…" : "Add books"}
            <input
              ref={picker}
              type="file"
              multiple
              className="sr-only"
              accept={system.import_formats.map((format) => `.${format}`).join(",")}
              onChange={(event) => add(Array.from(event.target.files ?? []))}
            />
          </label>
        </div>
      </div>

      {total > 0 && (
        <aside
          id="library-shelves"
          aria-label="Narrow the shelf"
          className={
            (shelves ? "" : "hidden md:block ") +
            "mb-6 border-y border-rule px-2 py-5 md:sticky md:top-16 md:col-start-1 md:row-span-2 md:row-start-1 md:h-[calc(100dvh-4rem)] md:self-start md:mb-0 md:overflow-y-auto md:border-y-0 md:border-r md:px-3 md:py-7 [scrollbar-width:thin]"
          }
        >
          {facets.data && <Shelves facets={facets.data} library={library.data} view={view} onShow={show} />}
        </aside>
      )}

      <div className="min-w-0 px-5 pb-7 md:col-start-[-2] md:row-start-2 md:px-10 md:pb-9">
        {books.error && <Problem>The library could not be loaded: {books.error}</Problem>}
        {empty && (
          <Empty title="Your library is empty">
            Choose <strong className="font-semibold text-ink">Add books</strong> or drop files anywhere on this page.
            EPUB, MOBI, AZW3, PDF and {Math.max(system.import_formats.length - 4, 0)} other formats are accepted.
          </Empty>
        )}
        {total > 0 && (
          <div>
            {partial && books.data && (
              <Narrowing view={view} lead={`${shelf.length} of ${count(total, "book")}`} onShow={show} />
            )}
            {books.data && shelf.length === 0 && (
              <Empty title={searching ? `No book matches “${view.query.trim()}”` : "No book matches"}>
                {narrowed(view) ? (
                  <>
                    Nothing is left after narrowing.{" "}
                    <Button variant="quiet" onClick={() => show({ ...blank, query: view.query, sort: view.sort })}>
                      Show everything
                    </Button>
                  </>
                ) : (
                  "Try fewer words, or a different spelling."
                )}
              </Empty>
            )}
            {layout === "list" && shelf.length > 0 && (
              <ShelfTable
                books={shelf}
                sort={view.sort}
                onSort={(sort) => show({ ...view, sort })}
                ticked={ticked}
                onTick={tick}
              />
            )}
            {layout === "grid" && (
              <ul className="grid grid-cols-[repeat(auto-fill,minmax(8.75rem,1fr))] gap-x-7 gap-y-9">
                {shelf.map((book) => (
                  <ShelfBook
                    key={book.id}
                    book={book}
                    ticked={ticked.has(book.id)}
                    selecting={selecting}
                    onTick={() => tick(book.id)}
                  />
                ))}
              </ul>
            )}
            {selecting && (
              <Selection
                ticked={chosen.length}
                shown={shelf.length}
                known={library.data?.tags ?? []}
                sending={sending}
                busy={busy}
                connected={device.data?.connected}
                onAll={() => setTicked(new Set(shelf.map((book) => book.id)))}
                onNone={clear}
                onTag={tag}
                onSend={send}
                onLeave={() => (sending ? navigate("/kindle") : clear())}
              />
            )}
          </div>
        )}
      </div>
    </div>
  );
}
